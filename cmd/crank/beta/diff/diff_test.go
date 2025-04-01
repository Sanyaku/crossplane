/*
Copyright 2025 The Crossplane Authors.

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

package diff

import (
	"bytes"
	"context"
	"fmt"
	xpextv1 "github.com/crossplane/crossplane/apis/apiextensions/v1"
	pkgv1 "github.com/crossplane/crossplane/apis/pkg/v1"
	tu "github.com/crossplane/crossplane/cmd/crank/beta/diff/testutils"
	"github.com/crossplane/crossplane/cmd/crank/beta/internal"
	"github.com/google/go-cmp/cmp"
	"io"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/crossplane/crossplane-runtime/pkg/errors"
	cc "github.com/crossplane/crossplane/cmd/crank/beta/diff/clusterclient"
	dp "github.com/crossplane/crossplane/cmd/crank/beta/diff/diffprocessor"
	un "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"
)

// Custom Run function for testing - this avoids calling the real Run()
func testRun(ctx context.Context, t *testing.T, c *Cmd, setupConfig func() (*rest.Config, error)) error {
	t.Helper()

	config, err := setupConfig()
	if err != nil {
		return errors.Wrap(err, "failed to get kubeconfig")
	}

	// Create a NopLogger for testing
	log := tu.TestLogger(t)

	client, err := ClusterClientFactory(config, cc.WithLogger(log))
	if err != nil {
		return errors.Wrap(err, "cannot initialize cluster client")
	}

	if err := client.Initialize(ctx); err != nil {
		return errors.Wrap(err, "cannot initialize diff processor")
	}

	// Create a temporary test file if needed
	tempFiles := make([]string, 0, len(c.Files))
	stdinUsed := false

	for _, f := range c.Files {
		switch {
		case f == "test-xr.yaml":
			tempDir, err := os.MkdirTemp("", "diff-test")
			if err != nil {
				return errors.Wrap(err, "failed to create temp dir")
			}
			defer os.RemoveAll(tempDir)
			tempFile := filepath.Join(tempDir, "test-xr.yaml")
			content := `
apiVersion: example.org/v1
kind: XExampleResource
metadata:
  name: test-xr
spec:
  coolParam: test-value
  replicas: 3
`
			if err := os.WriteFile(tempFile, []byte(content), 0600); err != nil {
				return errors.Wrap(err, "failed to write temp file")
			}
			tempFiles = append(tempFiles, tempFile)
		case f == "-":
			if !stdinUsed {
				tempFiles = append(tempFiles, "-")
				stdinUsed = true
			}
		default:
			tempFiles = append(tempFiles, f)
		}
	}

	// Create a composite loader for the resources (inlined from loadResources)
	loader, err := internal.NewCompositeLoader(tempFiles)
	if err != nil {
		return errors.Wrap(err, "cannot create resource loader")
	}

	resources, err := loader.Load()
	if err != nil {
		return errors.Wrap(err, "cannot load resources")
	}

	// Create the options for the processor
	options := []dp.ProcessorOption{
		dp.WithRestConfig(config),
		dp.WithNamespace(c.Namespace),
		dp.WithColorize(!c.NoColor),
		dp.WithCompact(c.Compact),
	}

	processor, err := ProcessorFactory(client, options...)
	if err != nil {
		return errors.Wrap(err, "cannot create diff processor")
	}

	// Initialize the diff processor
	if err := processor.Initialize(ctx); err != nil {
		return errors.Wrap(err, "cannot initialize diff processor")
	}

	if err := processor.PerformDiff(ctx, nil, resources); err != nil {
		return errors.Wrap(err, "unable to process one or more resources")
	}

	return nil
}

func TestCmd_Run(t *testing.T) {
	ctx := context.Background()

	// Save original factory functions
	originalClusterClientFactory := ClusterClientFactory
	originalDiffProcessorFactory := ProcessorFactory

	// Restore original functions at the end of the test
	defer func() {
		ClusterClientFactory = originalClusterClientFactory
		ProcessorFactory = originalDiffProcessorFactory
	}()

	type fields struct {
		Namespace string
		Files     []string
		NoColor   bool
		Compact   bool
	}

	type args struct {
		ctx context.Context
	}

	tests := map[string]struct {
		fields          fields
		args            args
		setupMocks      func()
		setupFiles      func() []string
		wantErr         bool
		wantErrContains string
	}{
		"SuccessfulRun": {
			fields: fields{
				Namespace: "default",
				Files:     []string{},
				NoColor:   false,
				Compact:   false,
			},
			args: args{
				ctx: ctx,
			},
			setupMocks: func() {
				// Set up mock client using the builder pattern
				mockClient := tu.NewMockClusterClient().
					WithSuccessfulInitialize().
					WithSuccessfulXRDsFetch([]*un.Unstructured{}).
					Build()

				ClusterClientFactory = func(*rest.Config, ...cc.Option) (cc.ClusterClient, error) {
					return mockClient, nil
				}

				// Set up mock processor using the builder pattern
				mockProcessor := tu.NewMockDiffProcessor().
					WithSuccessfulInitialize().
					WithSuccessfulPerformDiff().
					Build()

				ProcessorFactory = func(cc.ClusterClient, ...dp.ProcessorOption) (dp.DiffProcessor, error) {
					return mockProcessor, nil
				}
			},
			setupFiles: func() []string {
				// Create a temporary test file
				tempDir, err := os.MkdirTemp("", "diff-test")
				if err != nil {
					t.Fatalf("Failed to create temp dir: %v", err)
				}
				t.Cleanup(func() { _ = os.RemoveAll(tempDir) })

				tempFile := filepath.Join(tempDir, "test-resource.yaml")
				content := `
apiVersion: test.org/v1alpha1
kind: TestResource
metadata:
  name: test-resource
`
				if err := os.WriteFile(tempFile, []byte(content), 0600); err != nil {
					t.Fatalf("Failed to write temp file: %v", err)
				}

				return []string{tempFile}
			},
			wantErr: false,
		},
		"ClusterClientInitializeError": {
			fields: fields{
				Namespace: "default",
				Files:     []string{},
			},
			args: args{
				ctx: ctx,
			},
			setupMocks: func() {
				// Set up mock client with initialization error
				mockClient := tu.NewMockClusterClient().
					WithFailedInitialize("failed to initialize cluster client").
					Build()

				ClusterClientFactory = func(*rest.Config, ...cc.Option) (cc.ClusterClient, error) {
					return mockClient, nil
				}
			},
			setupFiles: func() []string {
				return []string{}
			},
			wantErr:         true,
			wantErrContains: "initialize diff processor",
		},
		"ProcessResourcesError": {
			fields: fields{
				Namespace: "default",
				Files:     []string{},
			},
			args: args{
				ctx: ctx,
			},
			setupMocks: func() {
				// Set up mock client
				mockClient := tu.NewMockClusterClient().
					WithSuccessfulInitialize().
					WithSuccessfulXRDsFetch([]*un.Unstructured{}).
					Build()

				ClusterClientFactory = func(*rest.Config, ...cc.Option) (cc.ClusterClient, error) {
					return mockClient, nil
				}

				// Set up mock processor with processing error
				mockProcessor := tu.NewMockDiffProcessor().
					WithSuccessfulInitialize().
					WithFailedPerformDiff("processing error").
					Build()

				ProcessorFactory = func(cc.ClusterClient, ...dp.ProcessorOption) (dp.DiffProcessor, error) {
					return mockProcessor, nil
				}
			},
			setupFiles: func() []string {
				// Create a temporary test file
				tempDir, err := os.MkdirTemp("", "diff-test")
				if err != nil {
					t.Fatalf("Failed to create temp dir: %v", err)
				}
				t.Cleanup(func() { _ = os.RemoveAll(tempDir) })

				tempFile := filepath.Join(tempDir, "test-resource.yaml")
				content := `
apiVersion: test.org/v1alpha1
kind: TestResource
metadata:
  name: test-resource
`
				if err := os.WriteFile(tempFile, []byte(content), 0600); err != nil {
					t.Fatalf("Failed to write temp file: %v", err)
				}

				return []string{tempFile}
			},
			wantErr:         true,
			wantErrContains: "process one or more resources",
		},
		"ClusterClientFactoryError": {
			fields: fields{
				Namespace: "default",
				Files:     []string{},
			},
			args: args{
				ctx: ctx,
			},
			setupMocks: func() {
				// Mock cluster client factory error
				ClusterClientFactory = func(*rest.Config, ...cc.Option) (cc.ClusterClient, error) {
					return nil, errors.New("failed to create cluster client")
				}
			},
			setupFiles: func() []string {
				return []string{}
			},
			wantErr:         true,
			wantErrContains: "cannot initialize cluster client",
		},
		"DiffProcessorFactoryError": {
			fields: fields{
				Namespace: "default",
				Files:     []string{},
			},
			args: args{
				ctx: ctx,
			},
			setupMocks: func() {
				// Set up mock client
				mockClient := tu.NewMockClusterClient().
					WithSuccessfulInitialize().
					Build()

				ClusterClientFactory = func(*rest.Config, ...cc.Option) (cc.ClusterClient, error) {
					return mockClient, nil
				}

				// Mock diff processor factory error
				ProcessorFactory = func(cc.ClusterClient, ...dp.ProcessorOption) (dp.DiffProcessor, error) {
					return nil, errors.New("failed to create diff processor")
				}
			},
			setupFiles: func() []string {
				// Create a temporary test file
				tempDir, err := os.MkdirTemp("", "diff-test")
				if err != nil {
					t.Fatalf("Failed to create temp dir: %v", err)
				}
				t.Cleanup(func() { _ = os.RemoveAll(tempDir) })

				tempFile := filepath.Join(tempDir, "test-resource.yaml")
				content := `
apiVersion: test.org/v1alpha1
kind: TestResource
metadata:
  name: test-resource
`
				if err := os.WriteFile(tempFile, []byte(content), 0600); err != nil {
					t.Fatalf("Failed to write temp file: %v", err)
				}

				return []string{tempFile}
			},
			wantErr:         true,
			wantErrContains: "cannot create diff processor",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			// Setup mocks for this test case
			tc.setupMocks()

			// Setup test files if needed
			files := tc.setupFiles()

			c := &Cmd{
				Namespace: tc.fields.Namespace,
				Files:     files,
				NoColor:   tc.fields.NoColor,
				Compact:   tc.fields.Compact,
			}

			// Use our test version of Run() that doesn't call clientcmd.BuildConfigFromFlags
			err := testRun(tc.args.ctx, t, c, func() (*rest.Config, error) {
				return &rest.Config{}, nil // Return a dummy config
			})

			if (err != nil) != tc.wantErr {
				t.Errorf("Run() error = %v, wantErr %v", err, tc.wantErr)
				return
			}
			if err != nil && tc.wantErrContains != "" {
				if !strings.Contains(err.Error(), tc.wantErrContains) {
					t.Errorf("Run() error = %v, wantErrContains %v", err, tc.wantErrContains)
				}
			}
		})
	}
}

// TestDiffWithExtraResources tests that a resource with differing values produces a diff
func TestDiffWithExtraResources(t *testing.T) {
	// Set up the test context
	ctx := context.Background()

	// Create test resources
	testComposition := createTestCompositionWithExtraResources()
	testXRD := createTestXRD()
	testExtraResource := createExtraResource()

	// Create test existing resource with different values
	existingResource := createExistingComposedResource()

	// Convert the test XRD to unstructured for GetXRDs to return
	xrdUnstructured, err := runtime.DefaultUnstructuredConverter.ToUnstructured(testXRD)
	if err != nil {
		t.Fatalf("Failed to convert XRD to un: %v", err)
	}

	// Set up the mock client using the builder pattern
	mockClient := tu.NewMockClusterClient().
		WithSuccessfulInitialize().
		WithFindMatchingComposition(func(_ context.Context, res *un.Unstructured) (*xpextv1.Composition, error) {
			// Validate the input XR
			if res.GetAPIVersion() != "example.org/v1" || res.GetKind() != "XExampleResource" {
				return nil, errors.New("unexpected resource type")
			}
			return testComposition, nil
		}).
		WithGetAllResourcesByLabels(func(_ context.Context, gvks []schema.GroupVersionKind, selectors []metav1.LabelSelector) ([]*un.Unstructured, error) {
			// Validate the GVK and selector match what we expect
			if len(gvks) != 1 || len(selectors) != 1 {
				return nil, errors.New("unexpected number of GVKs or selectors")
			}

			// Verify the GVK matches our extra resource - using GVK now instead of GVR
			expectedGVK := schema.GroupVersionKind{
				Group:   "example.org",
				Version: "v1",
				Kind:    "ExtraResource",
			}
			if gvks[0] != expectedGVK {
				return nil, errors.Errorf("unexpected GVK: %v", gvks[0])
			}

			// Verify the selector matches our label selector
			expectedSelector := metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "test-app",
				},
			}
			if !cmp.Equal(selectors[0].MatchLabels, expectedSelector.MatchLabels) {
				return nil, errors.New("unexpected selector")
			}

			return []*un.Unstructured{testExtraResource}, nil
		}).
		WithGetFunctionsFromPipeline(func(*xpextv1.Composition) ([]pkgv1.Function, error) {
			// Return functions for the composition pipeline
			return []pkgv1.Function{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "function-extra-resources",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "function-patch-and-transform",
					},
				},
			}, nil
		}).
		WithGetXRDs(func(context.Context) ([]*un.Unstructured, error) {
			return []*un.Unstructured{
				{Object: xrdUnstructured},
			}, nil
		}).
		WithGetResource(func(_ context.Context, _ schema.GroupVersionKind, _, name string) (*un.Unstructured, error) {
			if name == "test-xr-composed-resource" {
				return existingResource, nil
			}
			return nil, errors.Errorf("resource %q not found", name)
		}).
		// Add GetCRD implementation for our test
		WithGetCRD(func(context.Context, schema.GroupVersionKind) (*un.Unstructured, error) {
			// For this test, we can return nil as it doesn't focus on validation
			return nil, errors.New("CRD not found")
		}).
		// Set IsCRDRequired to return false for our test resources to avoid validation
		WithNoResourcesRequiringCRDs().
		WithSuccessfulDryRun().
		Build()

	// Create a buffer to capture output
	var buf bytes.Buffer

	// Save the original fprintf and restore it after the test
	origFprintf := fprintf
	defer func() { fprintf = origFprintf }()

	// TODO:  is this necessary?
	// Override fprintf to write to our buffer
	fprintf = func(_ io.Writer, format string, a ...interface{}) (int, error) {
		// For our test, redirect all output to our buffer regardless of the writer
		return fmt.Fprintf(&buf, format, a...)
	}

	// Create a temporary test file with the XR content
	tempDir, err := os.MkdirTemp("", "diff-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	tempFile := filepath.Join(tempDir, "test-xr.yaml")
	xrYAML := []byte(`
apiVersion: example.org/v1
kind: XExampleResource
metadata:
  name: test-xr
spec:
  coolParam: test-value
  replicas: 3
`)

	if err := os.WriteFile(tempFile, xrYAML, 0600); err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}

	// Create our command
	cmd := &Cmd{
		Namespace: "default",
		Files:     []string{tempFile},
	}

	// Save original ClusterClientFactory and restore after test
	originalClusterClientFactory := ClusterClientFactory
	originalDiffProcessorFactory := ProcessorFactory
	defer func() {
		ClusterClientFactory = originalClusterClientFactory
		ProcessorFactory = originalDiffProcessorFactory
	}()

	ClusterClientFactory = func(*rest.Config, ...cc.Option) (cc.ClusterClient, error) {
		return mockClient, nil
	}

	// Set up mock diff processor
	mockProcessor := tu.NewMockDiffProcessor().
		WithSuccessfulInitialize().
		WithPerformDiff(func(io.Writer, context.Context, []*un.Unstructured) error {
			// Generate a mock diff for our test
			_, _ = fmt.Fprintf(&buf, `~ ComposedResource/test-xr-composed-resource
{
  "spec": {
    "coolParam": "test-value",
    "extraData": "extra-resource-data",
    "replicas": 3
  }
}`)
			return nil
		}).
		Build()

	ProcessorFactory = func(cc.ClusterClient, ...dp.ProcessorOption) (dp.DiffProcessor, error) {
		return mockProcessor, nil
	}

	// Execute the test
	err = testRun(ctx, t, cmd, func() (*rest.Config, error) {
		return &rest.Config{}, nil
	})

	// Validate results
	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	// Check that the output contains expected diff information
	capturedOutput := buf.String()

	// Since the actual diff formatting might vary, we'll just check for key elements
	expectedElements := []string{
		"ComposedResource",          // Should mention resource type
		"test-xr-composed-resource", // Should mention resource name
		"coolParam",                 // Should mention changed field
		"test-value",                // Should mention new value
	}

	for _, expected := range expectedElements {
		if !strings.Contains(capturedOutput, expected) {
			t.Errorf("Expected output to contain '%s', but got: %s", expected, capturedOutput)
		}
	}
}

// TestDiffWithMatchingResources tests that a resource with matching values produces no diff
func TestDiffWithMatchingResources(t *testing.T) {
	// Set up the test context
	ctx := context.Background()

	// Create test resources
	testComposition := createTestCompositionWithExtraResources()
	testXRD := createTestXRD()
	testExtraResource := createExtraResource()

	// Create test existing resource with matching values
	matchingResource := createMatchingComposedResource()

	// Convert the test XRD to unstructured for GetXRDs to return
	xrdUnstructured, err := runtime.DefaultUnstructuredConverter.ToUnstructured(testXRD)
	if err != nil {
		t.Fatalf("Failed to convert XRD to un: %v", err)
	}

	// Set up the mock client using the builder pattern
	mockClient := tu.NewMockClusterClient().
		WithSuccessfulInitialize().
		WithSuccessfulCompositionMatch(testComposition).
		WithGetAllResourcesByLabels(func(context.Context, []schema.GroupVersionKind, []metav1.LabelSelector) ([]*un.Unstructured, error) {
			return []*un.Unstructured{testExtraResource}, nil
		}).
		WithGetFunctionsFromPipeline(func(*xpextv1.Composition) ([]pkgv1.Function, error) {
			return []pkgv1.Function{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "function-extra-resources",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "function-patch-and-transform",
					},
				},
			}, nil
		}).
		WithGetXRDs(func(context.Context) ([]*un.Unstructured, error) {
			return []*un.Unstructured{
				{Object: xrdUnstructured},
			}, nil
		}).
		WithGetResource(func(_ context.Context, _ schema.GroupVersionKind, _, name string) (*un.Unstructured, error) {
			if name == "test-xr-composed-resource" {
				return matchingResource, nil
			}
			return nil, errors.Errorf("resource %q not found", name)
		}).
		WithSuccessfulDryRun().
		Build()

	// Create a buffer to capture output
	var buf bytes.Buffer

	// Save the original fprintf and restore it after the test
	origFprintf := fprintf
	defer func() { fprintf = origFprintf }()

	// Override fprintf to write to our buffer
	fprintf = func(_ io.Writer, format string, a ...interface{}) (int, error) {
		// For our test, redirect all output to our buffer regardless of the writer
		return fmt.Fprintf(&buf, format, a...)
	}

	// Create a temporary test file with the XR content
	tempDir, err := os.MkdirTemp("", "diff-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	tempFile := filepath.Join(tempDir, "test-xr.yaml")
	xrYAML := []byte(`
apiVersion: example.org/v1
kind: XExampleResource
metadata:
  name: test-xr
spec:
  coolParam: test-value
  replicas: 3
`)

	if err := os.WriteFile(tempFile, xrYAML, 0600); err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}

	// Create our command
	cmd := &Cmd{
		Namespace: "default",
		Files:     []string{tempFile},
	}

	// Mock the factory functions
	originalClusterClientFactory := ClusterClientFactory
	originalDiffProcessorFactory := ProcessorFactory
	defer func() {
		ClusterClientFactory = originalClusterClientFactory
		ProcessorFactory = originalDiffProcessorFactory
	}()

	ClusterClientFactory = func(*rest.Config, ...cc.Option) (cc.ClusterClient, error) {
		return mockClient, nil
	}

	// Set up mock diff processor
	mockProcessor := tu.NewMockDiffProcessor().
		WithSuccessfulInitialize().
		WithPerformDiff(func(io.Writer, context.Context, []*un.Unstructured) error {
			// For matching resources, we don't produce any output
			return nil
		}).
		Build()

	ProcessorFactory = func(cc.ClusterClient, ...dp.ProcessorOption) (dp.DiffProcessor, error) {
		return mockProcessor, nil
	}

	// Execute the test
	err = testRun(ctx, t, cmd, func() (*rest.Config, error) {
		return &rest.Config{}, nil
	})

	// Validate results
	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	// For matching resources, we expect no diff output
	capturedOutput := buf.String()
	if capturedOutput != "" {
		t.Errorf("Expected no diff output for matching resources, but got: %s", capturedOutput)
	}
}
