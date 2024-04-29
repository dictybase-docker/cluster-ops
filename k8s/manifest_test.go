package k8s

import (
	"fmt"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/stretchr/testify/assert"
)

func TestContainer(t *testing.T) {
	tests := []struct {
		name          string
		expectedValue string
	}{
		{
			name:          "test1",
			expectedValue: "test1-container",
		},
		{
			name:          "example",
			expectedValue: "example-container",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := Container(test.name)
			assert.Equal(t, pulumi.String(test.expectedValue), result)
		})
	}
}

func TestImage(t *testing.T) {
	tests := []struct {
		image         string
		tag           string
		expectedValue string
	}{
		{
			image:         "alpine",
			tag:           "latest",
			expectedValue: "alpine:latest",
		},
		{
			image:         "nginx",
			tag:           "1.19",
			expectedValue: "nginx:1.19",
		},
	}

	for _, test := range tests {
		t.Run(fmt.Sprintf("%s:%s", test.image, test.tag), func(t *testing.T) {
			result := Image(test.image, test.tag)
			assert.Equal(t, pulumi.String(test.expectedValue), result)
		})
	}
}

func TestTemplateMetadata(t *testing.T) {
	tests := []struct {
		name          string
		expectedName  string
		expectedLabel string
	}{
		{
			name:          "template1",
			expectedName:  "template1-template",
			expectedLabel: "template1",
		},
		{
			name:          "exampleTemplate",
			expectedName:  "exampleTemplate-template",
			expectedLabel: "exampleTemplate",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := TemplateMetadata(test.name)
			assert.Equal(t, pulumi.String(test.expectedName), result.Name)
			assert.Equal(
				t,
				pulumi.StringMap{"app": pulumi.String(test.expectedLabel)},
				result.Labels,
			)
		})
	}
}

func TestMetadata(t *testing.T) {
	tests := []struct {
		namespace     string
		name          string
		expectedName  string
		expectedLabel string
	}{
		{
			namespace:     "default",
			name:          "app1",
			expectedName:  "app1",
			expectedLabel: "app1-pulumi",
		},
		{
			namespace:     "test",
			name:          "app2",
			expectedName:  "app2",
			expectedLabel: "app2-pulumi",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := Metadata(test.namespace, test.name)
			assert.Equal(
				t,
				pulumi.String(test.expectedName),
				result.Name,
			)
			assert.Equal(
				t,
				pulumi.StringMap{"app": pulumi.String(test.expectedLabel)},
				result.Labels,
			)
		},
		)
	}
}
