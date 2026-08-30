package state

import (
	"bytes"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

const (
	maxYAMLBytes       = 1 << 20
	maxYAMLDepth       = 32
	maxYAMLNodes       = 10_000
	maxYAMLScalarBytes = 64 << 10
)

// decodeStrict rejects unknown fields and duplicate mapping keys before decoding.
func decodeStrict(raw []byte, out any) error {
	if len(raw) == 0 || len(raw) > maxYAMLBytes {
		return newStateError("invalid_yaml", "YAML document has invalid size", nil)
	}
	var node yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&node); err != nil {
		return newStateError("invalid_yaml", "invalid YAML", err)
	}
	if node.Kind != yaml.DocumentNode || len(node.Content) != 1 || node.Content[0].Kind != yaml.MappingNode {
		return newStateError("invalid_yaml", "YAML document must be a mapping", nil)
	}
	if err := validateYAMLShape(&node, 0, new(int)); err != nil {
		return newStateError("invalid_yaml", err.Error(), err)
	}
	if err := rejectDuplicateKeys(&node); err != nil {
		return newStateError("duplicate_key", err.Error(), err)
	}
	// Node.Decode bypasses Decoder.KnownFields, so decode its canonical node
	// through a second strict decoder to reject misspelled schema keys.
	canonical, err := yaml.Marshal(&node)
	if err != nil {
		return newStateError("invalid_yaml", "cannot normalize YAML", err)
	}
	strict := yaml.NewDecoder(bytes.NewReader(canonical))
	strict.KnownFields(true)
	if err := strict.Decode(out); err != nil {
		return newStateError("invalid_yaml", "invalid YAML schema", err)
	}
	if err := strict.Decode(&struct{}{}); err != io.EOF {
		return newStateError("invalid_yaml", "YAML must contain one document", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return newStateError("invalid_yaml", "YAML must contain one document", err)
	}
	return nil
}
func validateYAMLShape(n *yaml.Node, depth int, nodes *int) error {
	*nodes++
	if *nodes > maxYAMLNodes {
		return fmt.Errorf("YAML document has too many nodes")
	}
	if depth > maxYAMLDepth {
		return fmt.Errorf("YAML document is nested too deeply")
	}
	if n.Kind == yaml.ScalarNode && len(n.Value) > maxYAMLScalarBytes {
		return fmt.Errorf("YAML scalar is too large")
	}
	for _, child := range n.Content {
		if err := validateYAMLShape(child, depth+1, nodes); err != nil {
			return err
		}
	}
	return nil
}

func rejectDuplicateKeys(n *yaml.Node) error {
	if n.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i < len(n.Content); i += 2 {
			key := n.Content[i].Value
			if seen[key] {
				return fmt.Errorf("duplicate YAML key %q", key)
			}
			seen[key] = true
			if err := rejectDuplicateKeys(n.Content[i+1]); err != nil {
				return err
			}
		}
	}
	for _, child := range n.Content {
		if err := rejectDuplicateKeys(child); err != nil {
			return err
		}
	}
	return nil
}
