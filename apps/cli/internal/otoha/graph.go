package otoha

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const graphRequestLimit = 65536

func GraphCommandHelp() Object {
	return Object{
		"schema": 1,
		"commands": []string{
			"otoha graph manifest TEAM",
			"otoha graph query TEAM --file QUERY.json",
			"otoha graph query TEAM --mode inspect --id NODE_ID",
			"otoha graph mutate TEAM --file MUTATION.json",
			"otoha graph result TEAM REQUEST_ID",
		},
		"query_example": Object{"mode": "filter", "kind": "node", "limit": 100},
		"mutation_example": Object{"request_id": "add-first-node", "expected_revision": 0,
			"operations": []Object{{"op": "node.add", "id": "first", "properties": Object{}}}},
		"identity": "Uses the joined team's existing project and credentials; no separate graph identity.",
		"recovery": "Mutate saves the original request before sending. After an unknown result, run graph result first. Result never resubmits. Explicit mutate retries must keep the same request ID and original content; no automatic background retry or complete offline graph is provided.",
	}
}

// UseNumber keeps the caller's JSON properties intact in the durable intent.
// Backend value support remains defined by the graph API, not by this parser.
func graphDecode(data []byte) (Object, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value Object
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if value == nil {
		return nil, errors.New("graph input must be a JSON object")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, errors.New("graph input must contain one JSON object")
	}
	return value, nil
}

func graphReadInput(path string) (Object, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, graphRequestLimit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > graphRequestLimit {
		return nil, errors.New("graph request exceeds 65536 bytes")
	}
	return graphDecode(data)
}

func graphLoadIntent(path string) (Object, error) {
	// Retain the existing private-file permission checks, then decode numbers
	// without rounding while comparing or resending the saved request.
	if _, err := Load(path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return graphDecode(data)
}

func graphIntentPath(kind, team, requestID string) string {
	return filepath.Join(Home(), "graph", kind, team, requestID+".json")
}

func graphExistingIntent(team, requestID string) (Object, error) {
	for _, kind := range []string{"pending", "receipts"} {
		record, err := graphLoadIntent(graphIntentPath(kind, team, requestID))
		if err == nil || !os.IsNotExist(err) {
			return record, err
		}
	}
	return nil, os.ErrNotExist
}

func graphIntentMatches(record Object, cfg TeamConfig, profile, request Object) error {
	if S(record, "project") != cfg.Project || S(record, "actor") != S(profile, "actor") || S(record, "authority") != strings.TrimRight(cfg.URL, "/") {
		return errors.New("saved graph request belongs to another project, identity or authority; original preserved")
	}
	if request != nil {
		original, err := json.Marshal(record["request"])
		if err != nil {
			return err
		}
		current, err := json.Marshal(request)
		if err != nil {
			return err
		}
		if !bytes.Equal(original, current) {
			return errors.New("graph request ID already has a different original intent; original preserved")
		}
	}
	return nil
}

func graphResultPath(cfg TeamConfig, requestID string) string {
	return "/v1/graph/requests/" + url.PathEscape(requestID) + "?project=" + url.QueryEscape(cfg.Project)
}

func graphStatus(team, requestID, state string) Object {
	return Object{"schema": "otoha-graph-cli/1", "team": team, "request_id": requestID, "state": state,
		"accepted": false, "next": "otoha graph result " + team + " " + requestID}
}

func graphFailure(team, requestID string, record Object, err error, mutationResponse bool) (Object, error) {
	state := "pending"
	var api *APIError
	if errors.As(err, &api) && mutationResponse && api.Status >= 400 && api.Status < 500 {
		state = "rejected"
	}
	if !mutationResponse && (record == nil || S(record, "state") != "pending") {
		state = "unknown"
	}
	result := graphStatus(team, requestID, state)
	result["error"] = err.Error()
	if api != nil {
		result["http_status"], result["error_code"] = api.Status, api.Code
	}
	if record != nil && S(record, "state") != "accepted" {
		record["state"], record["last_error"], record["updated_at"] = state, err.Error(), time.Now().UTC().Format(time.RFC3339Nano)
		if saveErr := Save(graphIntentPath("pending", team, requestID), record); saveErr != nil {
			return result, fmt.Errorf("%w; save request status: %v", err, saveErr)
		}
	}
	return result, err
}

func graphAccepted(team, requestID string, cfg TeamConfig, record, receipt Object) (Object, error) {
	_, sourceOK := eventInteger(receipt["source_version"])
	expected, expectedOK := eventInteger(receipt["expected_revision"])
	if record != nil {
		original, ok := O(record, "request")["expected_revision"].(json.Number)
		value, err := original.Int64()
		expectedOK = expectedOK && ok && err == nil && int64(expected) == value
	}
	if S(receipt, "schema") != "otoha-graph/1" || S(receipt, "state") != "accepted" || S(receipt, "request_id") != requestID || S(O(receipt, "scope"), "project") != cfg.Project || !sourceOK || !expectedOK {
		return graphFailure(team, requestID, record, errors.New("graph authority returned no matching accepted receipt; query the original request before retrying"), false)
	}
	if record != nil {
		record["state"], record["receipt"], record["updated_at"] = "accepted", receipt, time.Now().UTC().Format(time.RFC3339Nano)
		delete(record, "last_error")
		if err := Save(graphIntentPath("receipts", team, requestID), record); err != nil {
			return receipt, fmt.Errorf("graph accepted; save local receipt: %w", err)
		}
		if err := os.Remove(graphIntentPath("pending", team, requestID)); err != nil && !os.IsNotExist(err) {
			return receipt, fmt.Errorf("graph accepted; retire local pending request: %w", err)
		}
	}
	return receipt, nil
}

func graphMutation(ctx context.Context, cfg TeamConfig, profile Object, client *Client, request Object) (Object, error) {
	requestID := S(request, "request_id")
	if err := Name(requestID); err != nil {
		return nil, fmt.Errorf("graph mutation request_id: %w", err)
	}
	for key := range request {
		if key != "project" && key != "request_id" && key != "expected_revision" && key != "operations" {
			return nil, fmt.Errorf("unknown graph mutation field %q", key)
		}
	}
	revision, ok := request["expected_revision"].(json.Number)
	if !ok {
		return nil, errors.New("graph mutation requires a nonnegative integer expected_revision")
	}
	value, err := revision.Int64()
	if err != nil || value < 0 {
		return nil, errors.New("graph mutation requires a nonnegative integer expected_revision")
	}
	operations := Array(request["operations"])
	if len(operations) == 0 || len(operations) > 100 {
		return nil, errors.New("graph mutation requires 1 to 100 operations")
	}
	encoded, err := json.Marshal(request)
	if err != nil || len(encoded) > graphRequestLimit {
		return nil, errors.New("encoded graph mutation exceeds 65536 bytes")
	}
	record, err := graphExistingIntent(cfg.Name, requestID)
	existing := err == nil
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if existing {
		if err = graphIntentMatches(record, cfg, profile, request); err != nil {
			return nil, err
		}
		// An explicit retry checks the previous result before sending again.
		receipt, lookupErr := client.Call(ctx, http.MethodGet, graphResultPath(cfg, requestID), nil)
		if lookupErr == nil {
			return graphAccepted(cfg.Name, requestID, cfg, record, receipt)
		}
		var api *APIError
		if !errors.As(lookupErr, &api) || api.Status != 404 || api.Code != "graph_request_not_found" {
			return graphFailure(cfg.Name, requestID, record, lookupErr, false)
		}
		if S(record, "state") == "accepted" {
			result := graphStatus(cfg.Name, requestID, "unknown")
			result["local_state"], result["receipt_found"], result["error_code"] = "accepted", false, "graph_request_not_found"
			return result, errors.New("graph authority no longer returns a previously accepted receipt; local evidence preserved, request not resubmitted")
		}
	} else {
		record = Object{"schema": "otoha-graph-intent/1", "team": cfg.Name, "project": cfg.Project, "actor": profile["actor"], "authority": strings.TrimRight(cfg.URL, "/"), "request": request, "state": "pending", "created_at": time.Now().UTC().Format(time.RFC3339Nano)}
	}
	record["state"], record["updated_at"] = "pending", time.Now().UTC().Format(time.RFC3339Nano)
	delete(record, "last_error")
	if err = Save(graphIntentPath("pending", cfg.Name, requestID), record); err != nil {
		return nil, fmt.Errorf("persist graph request before sending: %w", err)
	}
	receipt, err := client.Call(ctx, http.MethodPost, "/v1/graph/mutations", request)
	if err != nil {
		return graphFailure(cfg.Name, requestID, record, err, true)
	}
	return graphAccepted(cfg.Name, requestID, cfg, record, receipt)
}

func GraphCommand(ctx context.Context, args []string) (Object, error) {
	if len(args) == 0 || args[0] == "help" {
		return GraphCommandHelp(), nil
	}
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			return GraphCommandHelp(), nil
		}
	}
	command := args[0]
	if command != "manifest" && command != "query" && command != "mutate" && command != "result" {
		return nil, errors.New("otoha graph manifest|query|mutate|result TEAM; run otoha graph help")
	}
	if len(args) < 2 {
		return nil, errors.New("graph command requires the joined TEAM name")
	}
	team := args[1]
	if err := Name(team); err != nil {
		return nil, err
	}
	input := Object{}
	requestID := ""
	switch command {
	case "manifest":
		if len(args) != 2 {
			return nil, errors.New("otoha graph manifest TEAM")
		}
	case "result":
		if len(args) != 3 {
			return nil, errors.New("otoha graph result TEAM REQUEST_ID")
		}
		requestID = args[2]
		if err := Name(requestID); err != nil {
			return nil, err
		}
	default:
		f := flag.NewFlagSet("graph "+command, flag.ContinueOnError)
		f.SetOutput(io.Discard)
		file := f.String("file", "", "JSON request file")
		mode := f.String("mode", "filter", "inspect, filter or neighborhood")
		id := f.String("id", "", "node or edge ID")
		kind := f.String("kind", "node", "node or edge")
		limit := f.Int("limit", 100, "maximum returned objects")
		if err := f.Parse(args[2:]); err != nil {
			return nil, err
		}
		if len(f.Args()) != 0 {
			return nil, errors.New("unexpected graph arguments")
		}
		if *file != "" {
			mixed := false
			f.Visit(func(value *flag.Flag) { mixed = mixed || value.Name != "file" })
			if mixed {
				return nil, errors.New("use --file without query flag overrides")
			}
			var err error
			input, err = graphReadInput(*file)
			if err != nil {
				return nil, err
			}
		} else if command == "mutate" {
			return nil, errors.New("otoha graph mutate TEAM --file MUTATION.json")
		} else {
			input = Object{"mode": *mode, "kind": *kind}
			switch *mode {
			case "inspect":
				limitSet := false
				f.Visit(func(value *flag.Flag) { limitSet = limitSet || value.Name == "limit" })
				if limitSet {
					return nil, errors.New("graph inspect does not accept --limit")
				}
				if *id == "" {
					return nil, errors.New("graph inspect requires --id")
				}
				input["id"] = *id
			case "filter":
				if *id != "" {
					return nil, errors.New("graph filter does not accept --id; use --mode inspect")
				}
				input["limit"] = *limit
			case "neighborhood":
				if *id == "" {
					return nil, errors.New("graph neighborhood requires --id")
				}
				input["id"], input["limit"] = *id, *limit
			default:
				return nil, errors.New("graph --mode must be inspect, filter or neighborhood")
			}
		}
	}
	cfg, err := loadTeamConfig(team)
	if err != nil {
		return nil, err
	}
	profile, client, err := teamProfile(cfg)
	if err != nil {
		return nil, err
	}
	defer client.Transport.Close()
	if cfg.Name != team || S(profile, "actor") == "" || strings.TrimRight(S(profile, "url"), "/") != strings.TrimRight(cfg.URL, "/") {
		return nil, errors.New("graph requires the joined team's matching identity and authority")
	}
	if err := Name(cfg.Project); err != nil {
		return nil, err
	}
	if project, present := input["project"]; present && project != cfg.Project {
		return nil, errors.New("graph input project differs from the joined team")
	}
	input["project"] = cfg.Project
	switch command {
	case "manifest":
		return client.Call(ctx, http.MethodGet, "/v1/graph/manifest?project="+url.QueryEscape(cfg.Project), nil)
	case "query":
		return client.Call(ctx, http.MethodPost, "/v1/graph/query", input)
	case "mutate":
		return graphMutation(ctx, cfg, profile, client, input)
	default:
		record, err := graphExistingIntent(team, requestID)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		if record != nil {
			if err = graphIntentMatches(record, cfg, profile, nil); err != nil {
				return nil, err
			}
		}
		receipt, err := client.Call(ctx, http.MethodGet, graphResultPath(cfg, requestID), nil)
		if err != nil {
			var api *APIError
			if errors.As(err, &api) && api.Status == 404 && api.Code == "graph_request_not_found" {
				result := graphStatus(team, requestID, "unknown")
				if record != nil {
					result["local_state"] = S(record, "state")
					if S(record, "state") == "pending" {
						result["state"] = "pending"
					}
				}
				result["receipt_found"] = false
				result["error_code"] = api.Code
				return result, err
			}
			return graphFailure(team, requestID, record, err, false)
		}
		return graphAccepted(team, requestID, cfg, record, receipt)
	}
}
