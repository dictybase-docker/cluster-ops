package kops

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"sort"

	"gopkg.in/yaml.v3"
)

// NormalizeYAMLFile reads a single or multi-document YAML file, strips server-managed
// metadata fields (creationTimestamp, generation), sorts multi-document instance groups
// alphabetically by name, and writes canonical normalized YAML to outputPath.
func NormalizeYAMLFile(inputPath, outputPath string) error {
	data, err := os.ReadFile(inputPath)
	if err != nil {
		return fmt.Errorf("read file %s: %w", inputPath, err)
	}

	norm, err := NormalizeYAML(data)
	if err != nil {
		return fmt.Errorf("normalize %s: %w", inputPath, err)
	}

	if err := os.WriteFile(outputPath, norm, 0o600); err != nil {
		return fmt.Errorf("write normalized file %s: %w", outputPath, err)
	}
	return nil
}

// NormalizeYAML strips server metadata and canonicalizes documents and mapping order.
func NormalizeYAML(data []byte) ([]byte, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var docs []map[string]any

	for {
		var doc map[string]any
		err := dec.Decode(&doc)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("decode yaml doc: %w", err)
		}
		if meta, ok := doc["metadata"].(map[string]any); ok {
			delete(meta, "creationTimestamp")
			delete(meta, "generation")
		}
		docs = append(docs, doc)
	}

	sort.SliceStable(docs, func(i, j int) bool {
		kindI, _ := docs[i]["kind"].(string)
		kindJ, _ := docs[j]["kind"].(string)
		if kindI != kindJ {
			return kindI < kindJ
		}
		nameI := docMetadataName(docs[i])
		nameJ := docMetadataName(docs[j])
		return nameI < nameJ
	})

	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	for _, doc := range docs {
		if err := enc.Encode(doc); err != nil {
			return nil, fmt.Errorf("encode normalized yaml doc: %w", err)
		}
	}
	return out.Bytes(), nil
}

func docMetadataName(doc map[string]any) string {
	if meta, ok := doc["metadata"].(map[string]any); ok {
		if name, ok := meta["name"].(string); ok {
			return name
		}
	}
	return ""
}
