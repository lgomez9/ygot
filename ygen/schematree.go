// Copyright 2017 Google Inc.
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

package ygen

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/openconfig/gnmi/ctree"
	"github.com/openconfig/goyang/pkg/yang"
	"github.com/openconfig/ygot/util"
)

// schemaTree contains a ctree.Tree that stores a copy of the YANG schema tree
// containing only leaf entries, such that schema paths can be referenced.
type schemaTree struct {
	ctree.Tree
}

// buildSchemaTree maps a set of yang.Entry pointers into a ctree structure.
// Only leaf or leaflist values are mapped, since these are the only entities
// that can be referenced by XPATH expressions within a YANG schema.
// It returns an error if there is duplication within the set of entries. The
// paths that are used within the schema are represented as a slice of strings.
func buildSchemaTree(entries []*yang.Entry) (*schemaTree, error) {
	t := &schemaTree{}
	for _, e := range entries {
		pp := strings.Split(e.Path(), "/")
		// We only want to find entities that are at the root of the
		// tree, since all children can be recursively mapped from
		// such entities. Since goyang's paths are of the form
		// /module-name/entity-name, then we just match length 3 of the
		// split string above.
		if len(pp) != 3 {
			continue
		}

		if !e.IsDir() {
			if err := t.Add([]string{pp[2]}, e); err != nil {
				return nil, err
			}
			continue
		}

		if err := schemaTreeChildrenAdd(t, e); err != nil {
			return nil, err
		}
	}
	return t, nil
}

// resolveLeafrefTarget takes an input path and context entry and
// determines the type of the leaf that is referred to by the path, such that
// it can be mapped to a native language type. It returns the yang.YangType that
// is associated with the target, and the target yang.Entry, such that the
// caller can map this to the relevant language type.
func (t *schemaTree) resolveLeafrefTarget(path string, contextEntry *yang.Entry) (*yang.Entry, error) {
	if t == nil {
		// This should not be possible if the calling code generation is
		// well structured and builds the schematree during parsing of YANG
		// files.
		return nil, fmt.Errorf("could not map leafref path: %v, from contextEntry: %v", path, contextEntry)
	}

	fixedPath, err := fixSchemaTreePath(path, contextEntry)
	if err != nil {
		return nil, err
	}

	e := t.GetLeafValue(fixedPath)
	if e == nil {
		return nil, fmt.Errorf("could not resolve leafref path: %v from %v, tree: %v", fixedPath, contextEntry, t)
	}

	target, ok := e.(*yang.Entry)
	if !ok {
		return nil, fmt.Errorf("invalid element returned from schema tree, must be a yang.Entry for path %v from %v", path, contextEntry)
	}

	return target, nil
}

// schemaTreeChildrenAdd adds the children of the supplied yang.Entry to the
// supplied ctree.Tree recursively.
func schemaTreeChildrenAdd(t *schemaTree, e *yang.Entry) error {
	for _, ch := range util.Children(e) {
		chPath := strings.Split(ch.Path(), "/")
		// chPath is of the form []string{"", "module", "entity", "child"}
		if !ch.IsDir() {
			if err := t.Add(chPath[2:], ch); err != nil {
				return err
			}
			continue
		}
		if err := schemaTreeChildrenAdd(t, ch); err != nil {
			return err
		}
	}
	return nil
}

// splitXPATHParts splits a YANG XPATH into a slice of strings, where each
// element in the slice is a part of the path as would be divided by a /
// within the XPATH. If attributes of a path element are specified, these are
// removed from the path (e.g., /interfaces/interface[name="eth0"] becomes
// []string{"interfaces", "interface"}.
func splitXPATHParts(path string) []string {
	// We cannot simply split on "/" since the path that we are supplied
	// with may be an XPATH that includes a /.
	var parts []string
	var buf bytes.Buffer
	var inKey bool
	for _, c := range path {
		switch c {
		case '/':
			if !inKey {
				parts = append(parts, buf.String())
				buf.Reset()
				continue
			}
		case '[':
			inKey = true
			continue
		case ']':
			inKey = false
			continue
		}
		// Make sure we don't append parts of the key to the path.
		if !inKey {
			buf.WriteRune(c)
		}
	}

	if buf.Len() != 0 {
		parts = append(parts, buf.String())
	}
	return parts
}

// removeXPATHNamespaces removes namespaces from a slice of strings that
// represents an split XPATH, i.e., []string{"oc-if:interfaces",
// "oc-if:interface"} becomes []string{"interfaces", "interface"}. It returns
// an error if invalid path element is encountered.
func removeXPATHNamespaces(path []string) ([]string, error) {
	var fixedParts []string
	// Remove namespaces, to ensure that the path is not "/namespace:element/namespace:elem2"
	// which is not how nodes are registered within the ctree.
	for _, p := range path {
		if strings.ContainsRune(p, ':') {
			sp := strings.Split(p, ":")
			if len(sp) != 2 {
				return nil, fmt.Errorf("invalid path element that contains multiple namespace specfiers: %v", p)
			}
			p = sp[1]
		}
		fixedParts = append(fixedParts, p)
	}
	return fixedParts, nil
}

// fixSchemaTreePath takes an input path from a YANG "path" statement - e.g.,
// /a/b/c/d and sanitises it for use in lookups within the schema tree. This
// includes:
//   - removing namespace prefixes from nodes.
//   - fully resolving relative paths.
func fixSchemaTreePath(path string, caller *yang.Entry) ([]string, error) {
	parts := splitXPATHParts(path)

	parts, err := removeXPATHNamespaces(parts)
	if err != nil {
		return nil, err
	}

	if parts[0] != ".." {
		if parts[0] == "" {
			return parts[1:], nil
		}
		return nil, fmt.Errorf("path statement has to begin with either '../' or '/': %s", path)
	}

	if caller == nil {
		return nil, fmt.Errorf("calling node must be specified when mapping relative path: %v", parts)
	}

	cpathparts := strings.Split(util.SchemaTreePath(caller), "/")
	if len(cpathparts) < 2 {
		// This caller was a module, which is not a valid context for an XPATH
		return nil, fmt.Errorf("invalid calling node with path %v, was a module: %v", caller.Path(), path)
	}
	callerPath := cpathparts[2:]
	var remainingPath []string
	for _, p := range parts {
		// If the element is ".." then we need to remove an element from the end of the
		// callerPath
		if p == ".." {
			if len(callerPath) == 0 {
				// We are at the stage where we are being asked to recurse above the
				// level of the caller, which is an error.
				return nil, fmt.Errorf("invalid path specified %v, for caller %v, tries to recurse above the root", path, caller.Path())
			}
			callerPath = callerPath[:len(callerPath)-1]
			continue
		}
		remainingPath = append(remainingPath, p)
	}
	parts = append(callerPath, remainingPath...)

	return parts, nil
}
