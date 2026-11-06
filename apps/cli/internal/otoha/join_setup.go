package otoha

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// Setup reports describe checks performed by this CLI invocation. They are
// neither proof of ongoing activity nor a user acceptance/read receipt.
func reportJoinedSetup(ctx context.Context, cfg TeamConfig, result, readiness Object) error {
	profile, client, err := teamProfile(cfg)
	if err != nil {
		return err
	}
	defer client.Transport.Close()
	manifest, err := client.Call(ctx, http.MethodGet, "/v1/manifest", nil)
	if err != nil {
		return err
	}
	if S(O(manifest, "actor"), "id") != S(profile, "actor") {
		return errors.New("setup report authority returned another actor")
	}
	result["checks_source"], result["user_accepted"] = "self_report", false
	if !setupReadyCapability(manifest) {
		// Alpha.2 and older teams can still join. Their authorities cannot
		// collect a typed team setup report, so do not call an unknown API.
		result["setup_state"], result["setup_ready_reported"] = "unsupported", false
		result["setup_note"] = "This authority does not support team setup reports; local join checks do not establish full-team readiness."
		return nil
	}
	graph, graphErr := client.Call(ctx, http.MethodGet, "/v1/graph/manifest?project="+url.QueryEscape(cfg.Project), nil)
	graphReady := graphErr == nil && S(graph, "schema") == "otoha-graph/1" && S(O(graph, "scope"), "project") == cfg.Project && B(graph, "ready")
	if graphReady {
		readiness["graph"] = "ready"
	} else {
		readiness["graph"] = "error"
		if graphErr == nil {
			graphErr = errors.New("graph manifest did not confirm this project's graph")
		}
	}
	checks := Object{"repository": S(readiness, "repository") == "ready", "service": S(readiness, "service") == "ready", "graph": graphReady, "communication": S(readiness, "communication") == "online"}
	allReady := true
	for _, value := range checks {
		allReady = allReady && value == true
	}
	report, err := client.Call(ctx, http.MethodPost, "/v1/setup-ready?project="+url.QueryEscape(cfg.Project), Object{"checks": checks})
	if err != nil {
		result["ready"], result["setup_state"], result["setup_ready_reported"] = false, "report_pending", false
		result["next"] = "Repeat the same join command to publish its setup checks; the saved identity and checkout are preserved."
		return fmt.Errorf("local setup checks completed; report not confirmed: %w", err)
	}
	receipt := O(report, "receipt")
	matching := S(report, "schema") == "otoha-setup-ready/1" && S(report, "project") == cfg.Project && S(receipt, "actor") == S(profile, "actor") && S(receipt, "device") == S(profile, "device") && S(receipt, "state") == "self_report" && !B(receipt, "user_accepted") && N(receipt, "reported_at") > 0 && B(receipt, "ready") == allReady
	for key, value := range checks {
		actual, ok := O(receipt, "checks")[key].(bool)
		matching = matching && ok && actual == value
	}
	if !matching {
		result["ready"], result["setup_state"], result["setup_ready_reported"] = false, "report_unconfirmed", false
		return errors.New("setup authority returned no matching self-report receipt; repeat the same join command")
	}
	result["setup_ready_receipt"], result["setup_ready_reported"] = receipt, true
	result["ready"] = allReady
	if allReady {
		result["setup_state"] = "complete"
		result["next"] = "Setup is complete. No extra message or Git write is required to finish joining; begin the team's work."
	} else {
		result["setup_state"] = "checks_incomplete"
		result["next"] = "Repeat the same join command after the failed setup check is available."
	}
	if graphErr != nil {
		return fmt.Errorf("project graph check failed: %w", graphErr)
	}
	return nil
}
