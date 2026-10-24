package otoha

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
)

// Older Python bundles allowed Infinity for a never-expiring invitation.
// Normalize only that JSON expires field, then discard bundled executable code.
func CompactBundle(path string) (Object, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	if len(b) > 1<<20 {
		return nil, fmt.Errorf("bundle exceeds 1 MiB")
	}
	b = regexp.MustCompile(`"expires"\s*:\s*Infinity`).ReplaceAll(b, []byte(`"expires":null`))
	var o Object
	if e = json.Unmarshal(b, &o); e != nil {
		return nil, e
	}
	delete(o, "client_files")
	delete(o, "client_files_sha256")
	if S(o, "project") == "" {
		grants := O(o, "grants")
		keys := []string{}
		for k := range grants {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if len(keys) != 1 {
			return nil, fmt.Errorf("legacy bundle requires an explicit single project")
		}
		o["project"] = keys[0]
	}
	if S(o, "channel") == "" {
		if S(o, "project") == "team" {
			o["channel"] = "general"
		} else {
			o["channel"] = S(o, "project") + "-general"
		}
	}
	return o, nil
}
