package diffprocessor

import (
	tu "github.com/crossplane/crossplane/cmd/crank/beta/diff/testutils"
	"k8s.io/client-go/rest"
	"testing"
)

func TestNewDiffProcessor(t *testing.T) {
	mockClient := &tu.MockClusterClient{}
	testConfig := &rest.Config{}

	tests := []struct {
		name        string
		client      *tu.MockClusterClient
		options     []DiffProcessorOption
		expectError bool
	}{
		{
			name:        "MissingRestConfig",
			client:      mockClient,
			options:     []DiffProcessorOption{},
			expectError: true,
		},
		//{  // in this test, it's a pointer to nil, which is not nil
		//	name:        "NilClient",
		//	client:      nil,
		//	options:     []DiffProcessorOption{WithRestConfig(testConfig)},
		//	expectError: true,
		//},
		{
			name:        "WithOptions",
			client:      mockClient,
			options:     []DiffProcessorOption{WithRestConfig(testConfig), WithNamespace("test"), WithColorize(false), WithCompact(true)},
			expectError: false,
		},
		{
			name:        "BasicOptions",
			client:      mockClient,
			options:     []DiffProcessorOption{WithRestConfig(testConfig)},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			processor, err := NewDiffProcessor(tt.client, tt.options...)

			if tt.expectError {
				if err == nil {
					t.Errorf("NewDiffProcessor() expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("NewDiffProcessor() unexpected error: %v", err)
				return
			}

			if processor == nil {
				t.Errorf("NewDiffProcessor() returned nil processor")
			}
		})
	}
}

func TestDiffOptions(t *testing.T) {
	tests := []struct {
		name     string
		config   ProcessorConfig
		expected DiffOptions
	}{
		{
			name: "DefaultOptions",
			config: ProcessorConfig{
				Colorize: true,
				Compact:  false,
			},
			expected: func() DiffOptions {
				opts := DefaultDiffOptions()
				opts.UseColors = true
				opts.Compact = false
				return opts
			}(),
		},
		{
			name: "NoColors",
			config: ProcessorConfig{
				Colorize: false,
				Compact:  false,
			},
			expected: func() DiffOptions {
				opts := DefaultDiffOptions()
				opts.UseColors = false
				opts.Compact = false
				return opts
			}(),
		},
		{
			name: "CompactDiff",
			config: ProcessorConfig{
				Colorize: true,
				Compact:  true,
			},
			expected: func() DiffOptions {
				opts := DefaultDiffOptions()
				opts.UseColors = true
				opts.Compact = true
				return opts
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.config.GetDiffOptions()

			if got.UseColors != tt.expected.UseColors {
				t.Errorf("GetDiffOptions().UseColors = %v, want %v", got.UseColors, tt.expected.UseColors)
			}

			if got.Compact != tt.expected.Compact {
				t.Errorf("GetDiffOptions().Compact = %v, want %v", got.Compact, tt.expected.Compact)
			}
		})
	}
}

func TestWithOptions(t *testing.T) {
	tests := []struct {
		name     string
		options  []DiffProcessorOption
		expected ProcessorConfig
	}{
		{
			name: "WithNamespace",
			options: []DiffProcessorOption{
				WithNamespace("test-namespace"),
			},
			expected: ProcessorConfig{
				Namespace: "test-namespace",
				Colorize:  true,  // Default
				Compact:   false, // Default
			},
		},
		{
			name: "WithMultipleOptions",
			options: []DiffProcessorOption{
				WithNamespace("test-namespace"),
				WithColorize(false),
				WithCompact(true),
			},
			expected: ProcessorConfig{
				Namespace: "test-namespace",
				Colorize:  false,
				Compact:   true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a default config
			config := ProcessorConfig{
				Namespace: "default",
				Colorize:  true,
				Compact:   false,
			}

			// Apply the options
			for _, option := range tt.options {
				option(&config)
			}

			// Check namespace
			if config.Namespace != tt.expected.Namespace {
				t.Errorf("Namespace = %v, want %v", config.Namespace, tt.expected.Namespace)
			}

			// Check colorize
			if config.Colorize != tt.expected.Colorize {
				t.Errorf("Colorize = %v, want %v", config.Colorize, tt.expected.Colorize)
			}

			// Check compact
			if config.Compact != tt.expected.Compact {
				t.Errorf("Compact = %v, want %v", config.Compact, tt.expected.Compact)
			}
		})
	}
}
