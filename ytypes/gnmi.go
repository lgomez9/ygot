// Copyright 2023 Google Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package ytypes

import (
	"fmt"
	"reflect"

	"github.com/openconfig/goyang/pkg/yang"
	"github.com/openconfig/ygot/util"
	"github.com/openconfig/ygot/ygot"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
)

// UnmarshalNotifications unmarshals a slice of Notifications on the root
// GoStruct specified by "schema". It *does not* perform validation after
// unmarshalling is complete.
//
// It does not make a copy and instead overwrites this value, so make a copy
// using ygot.DeepCopy() if you wish to retain the value at schema.Root prior
// to calling this function.
//
// If an error occurs during unmarshalling, schema.Root may already be
// modified. A rollback is not performed.
func UnmarshalNotifications(schema *Schema, ns []*gpb.Notification, opts ...UnmarshalOpt) error {
	for _, n := range ns {
		deletePaths := n.Delete
		if n.Atomic {
			deletePaths = append(deletePaths, &gpb.Path{})
		}
		err := UnmarshalSetRequest(schema, &gpb.SetRequest{
			Prefix: n.Prefix,
			Delete: deletePaths,
			Update: n.Update,
		}, opts...)
		if err != nil {
			return err
		}
	}
	return nil
}

// UnmarshalSetRequest applies a SetRequest on the root GoStruct specified by
// "schema". It *does not* perform validation after unmarshalling is complete.
//
// It does not make a copy and instead overwrites this value, so make a copy
// using ygot.DeepCopy() if you wish to retain the value at schema.Root prior
// to calling this function.
//
// If an error occurs during unmarshalling, schema.Root may already be
// modified. A rollback is not performed.
func UnmarshalSetRequest(schema *Schema, req *gpb.SetRequest, opts ...UnmarshalOpt) error {
	preferShadowPath := hasPreferShadowPath(opts)
	ignoreExtraFields := hasIgnoreExtraFields(opts)
	if req == nil {
		return nil
	}
	root := schema.Root
	var prefix *gpb.Path
	node, nodeName, err := getOrCreateNode(schema.RootSchema(), root, req.Prefix, preferShadowPath)
	if err != nil {
		// Fallback to prepending the prefix if getOrCreateNode failed.
		// This can happen if the prefix points to a compressed-out
		// node in compressed generated code. In particular this will
		// always happen for `telemetry-atomic` where the extension
		// marks such compressed out elements (i.e. surrounding
		// containers for lists and config/state containers).
		node = root
		nodeName = reflect.TypeOf(root).Elem().Name()
		prefix = req.Prefix
	}

	// Process deletes, then replace, then updates.
	if err := deletePaths(schema.SchemaTree[nodeName], node, prefix, req.Delete, preferShadowPath); err != nil {
		return err
	}
	if err := replacePaths(schema.SchemaTree[nodeName], node, prefix, req.Replace, preferShadowPath, ignoreExtraFields); err != nil {
		return err
	}
	if err := updatePaths(schema.SchemaTree[nodeName], node, prefix, req.Update, preferShadowPath, ignoreExtraFields); err != nil {
		return err
	}

	return nil
}

// getOrCreateNode instantiates the node at the given path, and returns that
// node along with its name.
func getOrCreateNode(schema *yang.Entry, goStruct ygot.GoStruct, path *gpb.Path, preferShadowPath bool) (ygot.GoStruct, string, error) {
	var gcopts []GetOrCreateNodeOpt
	if preferShadowPath {
		gcopts = append(gcopts, &PreferShadowPath{})
	}
	// Operate at the prefix level.
	nodeI, _, err := GetOrCreateNode(schema, goStruct, path, gcopts...)
	if err != nil {
		return nil, "", fmt.Errorf("failed to GetOrCreate the prefix node: %v", err)
	}
	node, ok := nodeI.(ygot.GoStruct)
	if !ok {
		return nil, "", fmt.Errorf("prefix path points to a non-GoStruct, this is not allowed: %T, %v", nodeI, nodeI)
	}

	return node, reflect.TypeOf(nodeI).Elem().Name(), nil
}

// deletePaths deletes a slice of paths from the given GoStruct.
func deletePaths(schema *yang.Entry, goStruct ygot.GoStruct, prefix *gpb.Path, paths []*gpb.Path, preferShadowPath bool) error {
	var dopts []DelNodeOpt
	if preferShadowPath {
		dopts = append(dopts, &PreferShadowPath{})
	}

	for _, path := range paths {
		if prefix != nil {
			var err error
			if path, err = util.JoinPaths(prefix, path); err != nil {
				return fmt.Errorf("cannot join prefix with deletion path: %v", err)
			}
		}
		if err := DeleteNode(schema, goStruct, path, dopts...); err != nil {
			return err
		}
	}
	return nil
}

// joinPrefixToUpdate returns a new update that has the prefix joined to the path.
//
// It guarantees to not change the original update, and preserves the .Val and
// .Path values.
func joinPrefixToUpdate(prefix *gpb.Path, update *gpb.Update) (*gpb.Update, error) {
	if prefix == nil {
		return update, nil
	}
	// Here we avoid doing a deep copy for performance.
	// Currently protobuf is missing a library function for a safe
	// shallow copy: https://github.com/golang/protobuf/issues/1155
	joinedUpdate := &gpb.Update{
		Val: update.Val,
	}
	var err error
	if joinedUpdate.Path, err = util.JoinPaths(prefix, update.Path); err != nil {
		return nil, fmt.Errorf("cannot join prefix with gpb.Update path: %v", err)
	}
	return joinedUpdate, nil
}

// replacePaths unmarshals a slice of updates into the given GoStruct. It
// deletes the values at these paths before unmarshalling them. These updates
// can either by JSON-encoded or gNMI-encoded values (scalars).
func replacePaths(schema *yang.Entry, goStruct ygot.GoStruct, prefix *gpb.Path, updates []*gpb.Update, preferShadowPath, ignoreExtraFields bool) error {
	var dopts []DelNodeOpt
	if preferShadowPath {
		dopts = append(dopts, &PreferShadowPath{})
	}

	for _, update := range updates {
		var err error
		if update, err = joinPrefixToUpdate(prefix, update); err != nil {
			return err
		}
		if err := DeleteNode(schema, goStruct, update.Path, dopts...); err != nil {
			return err
		}
		if err := setNode(schema, goStruct, update, preferShadowPath, ignoreExtraFields); err != nil {
			return err
		}
	}
	return nil
}

// updatePaths unmarshals a slice of updates into the given GoStruct. These
// updates can either by JSON-encoded or gNMI-encoded values (scalars).
func updatePaths(schema *yang.Entry, goStruct ygot.GoStruct, prefix *gpb.Path, updates []*gpb.Update, preferShadowPath, ignoreExtraFields bool) error {
	for _, update := range updates {
		var err error
		if update, err = joinPrefixToUpdate(prefix, update); err != nil {
			return err
		}
		if err := setNode(schema, goStruct, update, preferShadowPath, ignoreExtraFields); err != nil {
			return err
		}
	}
	return nil
}

// setNode unmarshals either a JSON-encoded value or a gNMI-encoded (scalar)
// value into the given GoStruct.
func setNode(schema *yang.Entry, goStruct ygot.GoStruct, update *gpb.Update, preferShadowPath, ignoreExtraFields bool) error {
	sopts := []SetNodeOpt{&InitMissingElements{}}
	if preferShadowPath {
		sopts = append(sopts, &PreferShadowPath{})
	}
	if ignoreExtraFields {
		sopts = append(sopts, &IgnoreExtraFields{})
	}

	if err := SetNode(schema, goStruct, update.Path, update.Val, sopts...); err != nil {
		return fmt.Errorf("setNode: %v", err)
	}
	return nil
}
