/*
Copyright AppsCode Inc. and Contributors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package utils

import (
	"bytes"
	"encoding/gob"
	"encoding/json"

	"gomodules.xyz/jsonpatch/v2"
)

func init() {
	gob.Register(map[string]interface{}{})
}

func DeepCopyMap(m map[string]interface{}) (map[string]interface{}, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	dec := gob.NewDecoder(&buf)
	err := enc.Encode(m)
	if err != nil {
		return nil, err
	}
	var cp map[string]interface{}
	err = dec.Decode(&cp)
	if err != nil {
		return nil, err
	}
	return cp, nil
}

func Copy(src any, dst any) error {
	jsonByte, err := json.Marshal(src)
	if err != nil {
		return err
	}
	return json.Unmarshal(jsonByte, dst)
}

func CreateJsonPatch(empty, custom interface{}) ([]byte, error) {
	emptyBytes, err := json.Marshal(empty)
	if err != nil {
		return nil, err
	}
	customBytes, err := json.Marshal(custom)
	if err != nil {
		return nil, err
	}

	patch, err := jsonpatch.CreatePatch(emptyBytes, customBytes)
	if err != nil {
		return nil, err
	}

	return json.MarshalIndent(patch, "", "  ")
}

// MergeMaps merges the default and override maps, with values from the override map taking precedence.
func MergeMaps(defaults, overrides map[string]interface{}) map[string]interface{} {
	merged := make(map[string]interface{})

	// First, copy all default values into the merged map.
	for key, value := range defaults {
		merged[key] = value
	}

	// Now, override with values from the overrides map.
	for key, value := range overrides {
		merged[key] = value
	}

	return merged
}
