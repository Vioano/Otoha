package otoha

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// teamCreateCommand registers another project on an already configured center.
// It keeps the source identity and transport in their original profile file.
func teamCreateCommand(ctx context.Context, args []string) (Object, error) {
	if len(args) == 0 {
		return nil, errors.New("otoha team create NAME --from-team SOURCE [--project ID] [--channel ID]")
	}
	name := args[0]
	if err := Name(name); err != nil {
		return nil, fmt.Errorf("invalid team name: %w", err)
	}
	f := teamFlagSet("team create")
	sourceName := f.String("from-team", "", "existing team whose owner identity and center to reuse")
	project := f.String("project", name, "project ID on the existing center")
	channel := f.String("channel", "", "channel ID; defaults to PROJECT-general")
	if err := f.Parse(args[1:]); err != nil {
		return nil, err
	}
	if f.NArg() != 0 || *sourceName == "" {
		return nil, errors.New("otoha team create NAME --from-team SOURCE [--project ID] [--channel ID]")
	}
	if err := Name(*sourceName); err != nil {
		return nil, fmt.Errorf("invalid source team: %w", err)
	}
	if name == *sourceName {
		return nil, errors.New("target team must differ from --from-team; original team preserved")
	}
	if err := Name(*project); err != nil {
		return nil, fmt.Errorf("invalid project: %w", err)
	}
	if *channel == "" {
		*channel = *project + "-general"
	}
	if err := Name(*channel); err != nil {
		return nil, fmt.Errorf("invalid channel: %w", err)
	}

	source, err := loadTeamConfig(*sourceName)
	if err != nil {
		return nil, fmt.Errorf("read source team: %w", err)
	}
	profilePath, err := filepath.Abs(source.Profile)
	if err != nil {
		return nil, fmt.Errorf("resolve existing profile: %w", err)
	}
	previous, previousErr := loadTeamConfig(name)
	exists := previousErr == nil
	if exists {
		previousProfile, pathErr := filepath.Abs(previous.Profile)
		if pathErr != nil || previousProfile != profilePath ||
			strings.TrimRight(previous.URL, "/") != strings.TrimRight(source.URL, "/") ||
			previous.Project != *project || previous.Channel != *channel {
			return nil, errors.New("target team alias belongs to another center, identity or project/channel; original configuration preserved")
		}
	} else if !os.IsNotExist(previousErr) {
		return nil, fmt.Errorf("read target team: %w", previousErr)
	}

	source.Profile = profilePath
	profile, client, err := teamProfile(source)
	if err != nil {
		return nil, fmt.Errorf("open source identity: %w", err)
	}
	defer client.Transport.Close()
	if strings.TrimRight(S(profile, "url"), "/") != strings.TrimRight(source.URL, "/") {
		return nil, errors.New("source team URL differs from its saved identity; original configuration preserved")
	}
	manifest, err := client.Call(ctx, http.MethodGet, "/v1/manifest", nil)
	if err != nil {
		return nil, fmt.Errorf("verify existing center identity: %w", err)
	}
	actor := O(manifest, "actor")
	if S(manifest, "protocol") != "otoha-collaboration/1" {
		return nil, errors.New("center did not return the Otoha collaboration protocol")
	}
	if S(profile, "actor") == "" || S(actor, "id") != S(profile, "actor") {
		return nil, errors.New("authenticated actor differs from the saved source identity; original identity preserved")
	}
	if !B(actor, "owner") {
		return nil, errors.New("center owner identity required to register a project")
	}

	_, projectExists, err := teamCreateLookup(ctx, client, "projects", *project)
	if err != nil {
		return nil, err
	}
	currentChannel, channelExists, err := teamCreateLookup(ctx, client, "channels", *channel)
	if err != nil {
		return nil, err
	}
	// Both reads precede either POST: a globally occupied channel must not
	// leave an unnecessary project behind.
	if channelExists && S(currentChannel, "project") != *project {
		return nil, fmt.Errorf("channel %q belongs to another project; no registration performed", *channel)
	}
	if !projectExists {
		if err = teamCreateRegister(ctx, client, "projects", *project,
			Object{"id": *project, "name": *project}, ""); err != nil {
			return nil, err
		}
	}
	if !channelExists {
		if err = teamCreateRegister(ctx, client, "channels", *channel,
			Object{"id": *channel, "project": *project, "name": *channel}, *project); err != nil {
			return nil, err
		}
	}

	cfg := previous
	if !exists {
		cfg = TeamConfig{Protocol: "otoha-team/1", Name: name, Mode: "member", URL: source.URL,
			Project: *project, Channel: *channel, State: "ready", Profile: profilePath,
			CreatedAt: time.Now().UTC().Format(time.RFC3339), StartedBy: "otoha team create", ManageCenter: false}
		if err := saveTeamConfig(cfg); err != nil {
			return nil, fmt.Errorf("project/channel registered but target team metadata was not saved: %w", err)
		}
	}
	return Object{"created": !exists, "registered": true, "team": name, "from_team": *sourceName,
		"mode": cfg.Mode, "state": cfg.State, "url": source.URL, "profile": profilePath,
		"project": *project, "channel": *channel, "actor": S(actor, "id"), "identity_reused": true}, nil
}

func teamCreateLookup(ctx context.Context, client *Client, collection, id string) (Object, bool, error) {
	result, err := client.Call(ctx, http.MethodGet, "/v1/"+collection, nil)
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", collection, err)
	}
	rows, ok := result[collection].([]any)
	if !ok {
		return nil, false, fmt.Errorf("center returned an invalid %s list", collection)
	}
	for _, row := range rows {
		item, ok := row.(map[string]any)
		if !ok || S(Object(item), "id") == "" {
			return nil, false, fmt.Errorf("center returned an invalid %s entry", collection)
		}
		if S(Object(item), "id") == id {
			return Object(item), true, nil
		}
	}
	return nil, false, nil
}

func teamCreateRegister(ctx context.Context, client *Client, collection, id string, body Object, project string) error {
	_, postErr := client.Call(ctx, http.MethodPost, "/v1/"+collection, body)
	item, found, readErr := teamCreateLookup(ctx, client, collection, id)
	if readErr != nil {
		if postErr != nil {
			return fmt.Errorf("%s registration unconfirmed (POST: %v; readback: %w)", collection, postErr, readErr)
		}
		return fmt.Errorf("%s registration unconfirmed: %w", collection, readErr)
	}
	if !found {
		if postErr != nil {
			return fmt.Errorf("%s registration not confirmed by readback: %w", collection, postErr)
		}
		return fmt.Errorf("%s POST returned success but %q is absent on readback", collection, id)
	}
	if collection == "channels" && S(item, "project") != project {
		return fmt.Errorf("channel %q belongs to another project on readback; target team not saved", id)
	}
	// A duplicate or an unknown outcome can be confirmed with the stable ID.
	// A definite rejection such as 403 remains a rejection even if another
	// operation happened to create the same ID before the readback.
	var api *APIError
	if errors.As(postErr, &api) && api.Status >= 400 && api.Status < 500 && api.Status != http.StatusConflict {
		return fmt.Errorf("%s registration rejected: %w", collection, postErr)
	}
	return nil
}
