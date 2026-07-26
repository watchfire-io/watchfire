package install

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

// mergeJSONEntry merges the watchfire server entry into the JSON object in
// raw, at the object path keys... (created as needed), under ServerName.
//
// Semantics are designed to never lose user content:
//   - empty/missing input starts a fresh object
//   - unparseable input (or a non-object on the path) returns an error —
//     callers degrade to manual instructions instead of overwriting
//   - an existing watchfire entry that already contains all of entry's
//     keys with equal values is left alone (ActionUnchanged, nil bytes)
//   - an existing watchfire object entry is merged key-by-key, preserving
//     any extra keys the user added (ActionUpdated)
//
// Note the rewrite normalizes formatting (two-space indent, sorted keys);
// all values are preserved.
func mergeJSONEntry(raw []byte, keys []string, entry map[string]any) ([]byte, Action, error) {
	root := map[string]any{}
	if trimmed := bytes.TrimSpace(raw); len(trimmed) > 0 {
		if err := json.Unmarshal(trimmed, &root); err != nil {
			return nil, ActionManual, fmt.Errorf("parse existing config: %w", err)
		}
	}

	node := root
	for _, k := range keys {
		child, ok := node[k]
		if !ok || child == nil {
			m := map[string]any{}
			node[k] = m
			node = m
			continue
		}
		m, ok := child.(map[string]any)
		if !ok {
			return nil, ActionManual, fmt.Errorf("existing config: %q is not an object", k)
		}
		node = m
	}

	want := normalizeJSON(entry)
	action := ActionInstalled
	if existing, ok := node[ServerName]; ok {
		if existingMap, ok := existing.(map[string]any); ok {
			if jsonSubset(want, existingMap) {
				return nil, ActionUnchanged, nil
			}
			for k, v := range want {
				existingMap[k] = v
			}
			action = ActionUpdated
		} else {
			node[ServerName] = want
			action = ActionUpdated
		}
	} else {
		node[ServerName] = want
	}

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, ActionManual, err
	}
	return append(out, '\n'), action, nil
}

// installJSONClient runs the read → merge → write cycle for a JSON-config
// client, degrading to a Manual result on parse or write failure.
func installJSONClient(configPath string, keys []string, entry map[string]any, snippet string) Result {
	raw, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return manualResult(configPath, fmt.Sprintf("cannot read %s: %v", configPath, err), snippet)
	}

	merged, action, err := mergeJSONEntry(raw, keys, entry)
	if err != nil {
		return manualResult(configPath, fmt.Sprintf("cannot safely edit %s: %v", configPath, err), snippet)
	}
	if action == ActionUnchanged {
		return Result{Action: ActionUnchanged, ConfigPath: configPath}
	}
	if err := writeConfig(configPath, merged); err != nil {
		return manualResult(configPath, fmt.Sprintf("cannot write %s: %v", configPath, err), snippet)
	}
	return Result{Action: action, ConfigPath: configPath}
}

// normalizeJSON round-trips v through encoding/json so comparisons see the
// same types the parsed config uses ([]any, float64, ...).
func normalizeJSON(v map[string]any) map[string]any {
	data, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return v
	}
	return out
}

// jsonSubset reports whether every key in want exists in have with an equal
// value (extra keys in have are ignored).
func jsonSubset(want, have map[string]any) bool {
	for k, v := range want {
		hv, ok := have[k]
		if !ok || !jsonEqual(v, hv) {
			return false
		}
	}
	return true
}

// jsonEqual compares two parsed-JSON values by their canonical encoding.
func jsonEqual(a, b any) bool {
	da, err := json.Marshal(a)
	if err != nil {
		return false
	}
	db, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return bytes.Equal(da, db)
}
