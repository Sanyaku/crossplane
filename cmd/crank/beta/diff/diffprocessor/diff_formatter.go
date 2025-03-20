package diffprocessor

import (
	"fmt"
	"github.com/crossplane/crossplane-runtime/pkg/errors"
	"github.com/sergi/go-diff/diffmatchpatch"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	sigsyaml "sigs.k8s.io/yaml"
	"strings"
)

// DiffType represents the type of diff (added, removed, modified)
type DiffType string

const (
	DiffTypeAdded    DiffType = "+"
	DiffTypeRemoved  DiffType = "-"
	DiffTypeModified DiffType = "~"
	DiffTypeEqual    DiffType = "="
)

// Colors for terminal output
const (
	ColorRed   = "\x1b[31m"
	ColorGreen = "\x1b[32m"
	ColorReset = "\x1b[0m"
)

// DiffOptions holds configuration options for the diff output
type DiffOptions struct {
	// UseColors determines whether to colorize the output
	UseColors bool

	// AddPrefix is the prefix for added lines (default "+")
	AddPrefix string

	// DeletePrefix is the prefix for deleted lines (default "-")
	DeletePrefix string

	// ContextPrefix is the prefix for unchanged lines (default " ")
	ContextPrefix string

	// ContextLines is the number of unchanged lines to show before/after changes in compact mode
	ContextLines int

	// ChunkSeparator is the string used to separate chunks in compact mode
	ChunkSeparator string

	// Compact determines whether to show a compact diff
	Compact bool
}

// ResourceDiff represents the diff for a specific resource
type ResourceDiff struct {
	ResourceKind string
	ResourceName string
	DiffType     DiffType
	LineDiffs    []diffmatchpatch.Diff
	Current      *unstructured.Unstructured // Optional, for reference
	Desired      *unstructured.Unstructured // Optional, for reference
}

// DefaultDiffOptions returns the default options with colors enabled
func DefaultDiffOptions() DiffOptions {
	return DiffOptions{
		UseColors:      true,
		AddPrefix:      "+ ",
		DeletePrefix:   "- ",
		ContextPrefix:  "  ",
		ContextLines:   3,
		ChunkSeparator: "...",
		Compact:        false,
	}
}

// CompactDiffOptions returns the default options with colors enabled
func CompactDiffOptions() DiffOptions {
	return DiffOptions{
		UseColors:      true,
		AddPrefix:      "+ ",
		DeletePrefix:   "- ",
		ContextPrefix:  "  ",
		ContextLines:   3,
		ChunkSeparator: "...",
		Compact:        true,
	}
}

// DiffFormatter is the interface that defines the contract for diff formatters
type DiffFormatter interface {
	Format(diffs []diffmatchpatch.Diff, options DiffOptions) string
}

// FullDiffFormatter formats diffs with all context lines
type FullDiffFormatter struct{}

// CompactDiffFormatter formats diffs with limited context lines
type CompactDiffFormatter struct{}

// NewFormatter returns a DiffFormatter based on whether compact mode is desired
func NewFormatter(compact bool) DiffFormatter {
	if compact {
		return &CompactDiffFormatter{}
	}
	return &FullDiffFormatter{}
}

// FormatDiff formats a slice of diffs according to the provided options
func FormatDiff(diffs []diffmatchpatch.Diff, options DiffOptions) string {
	// Use the appropriate formatter
	formatter := NewFormatter(options.Compact)
	return formatter.Format(diffs, options)
}

// Format implements the DiffFormatter interface for FullDiffFormatter
func (f *FullDiffFormatter) Format(diffs []diffmatchpatch.Diff, options DiffOptions) string {
	var builder strings.Builder

	for _, diff := range diffs {
		formattedLines, _ := processLines(diff, options)
		for _, line := range formattedLines {
			builder.WriteString(line)
			builder.WriteString("\n")
		}
	}

	return builder.String()
}

// Format implements the DiffFormatter interface for CompactDiffFormatter
func (f *CompactDiffFormatter) Format(diffs []diffmatchpatch.Diff, options DiffOptions) string {
	// Create a flat array of all formatted lines with their diff types
	type lineItem struct {
		Type      diffmatchpatch.Operation
		Content   string
		Formatted string
	}

	var allLines []lineItem

	for _, diff := range diffs {
		formattedLines, hasTrailingNewline := processLines(diff, options)

		for i, formatted := range formattedLines {
			// For non-trailing empty lines or regular lines
			content := ""
			if isEmptyTrailer := hasTrailingNewline && len(formattedLines) == 1 && i == 0; !isEmptyTrailer {
				content = strings.Split(diff.Text, "\n")[i]
			}

			allLines = append(allLines, lineItem{
				Type:      diff.Type,
				Content:   content,
				Formatted: formatted,
			})
		}
	}

	// Now build compact output with context
	var builder strings.Builder
	contextLines := options.ContextLines

	// Find change blocks (sequences of inserts/deletes)
	type changeBlock struct {
		StartIdx int
		EndIdx   int
	}

	var changeBlocks []changeBlock
	var currentBlock *changeBlock

	// Identify all the change blocks
	for i := 0; i < len(allLines); i++ {
		if allLines[i].Type != diffmatchpatch.DiffEqual {
			// Start a new block if we don't have one
			if currentBlock == nil {
				currentBlock = &changeBlock{StartIdx: i, EndIdx: i}
			} else {
				// Extend current block
				currentBlock.EndIdx = i
			}
		} else if currentBlock != nil {
			// If we were in a block and hit an equal line, finish the block
			changeBlocks = append(changeBlocks, *currentBlock)
			currentBlock = nil
		}
	}

	// Add the last block if it's still active
	if currentBlock != nil {
		changeBlocks = append(changeBlocks, *currentBlock)
	}

	// If we have no change blocks, return an empty string
	if len(changeBlocks) == 0 {
		return ""
	}

	// Keep track of the last line we printed
	lastPrintedIdx := -1

	// Now process each block with its context
	for blockIdx, block := range changeBlocks {
		// Calculate visible range for context before the block
		contextStart := max(0, block.StartIdx-contextLines)

		// If this isn't the first block, check if we need a separator
		if blockIdx > 0 {
			prevBlock := changeBlocks[blockIdx-1]
			prevContextEnd := min(len(allLines), prevBlock.EndIdx+contextLines+1)

			// If there's a gap between the end of the previous context and the start of this context,
			// add a separator
			if contextStart > prevContextEnd {
				// Add separator
				builder.WriteString(fmt.Sprintf("%s\n", options.ChunkSeparator))
				lastPrintedIdx = -1 // Reset to force printing of context lines
			} else {
				// Contexts overlap or are adjacent - adjust the start to avoid duplicate lines
				contextStart = max(lastPrintedIdx+1, contextStart)
			}
		}

		// Print context before the change if we haven't already printed it
		for i := contextStart; i < block.StartIdx; i++ {
			if i > lastPrintedIdx {
				builder.WriteString(allLines[i].Formatted)
				builder.WriteString("\n")
				lastPrintedIdx = i
			}
		}

		// Print the changes
		for i := block.StartIdx; i <= block.EndIdx; i++ {
			builder.WriteString(allLines[i].Formatted)
			builder.WriteString("\n")
			lastPrintedIdx = i
		}

		// Print context after the change
		contextEnd := min(len(allLines), block.EndIdx+contextLines+1)
		for i := block.EndIdx + 1; i < contextEnd; i++ {
			builder.WriteString(allLines[i].Formatted)
			builder.WriteString("\n")
			lastPrintedIdx = i
		}
	}

	return builder.String()
}

// GetLineDiff performs a proper line-by-line diff and returns the raw diffs
func GetLineDiff(oldText, newText string) []diffmatchpatch.Diff {
	patch := diffmatchpatch.New()

	// Use the line-to-char conversion to treat each line as an atomic unit
	ch1, ch2, lines := patch.DiffLinesToChars(oldText, newText)

	diff := patch.DiffMain(ch1, ch2, false)
	patch.DiffCleanupSemantic(diff)

	return patch.DiffCharsToLines(diff, lines)
}

// GenerateDiffWithOptions produces a structured diff between two unstructured objects
func GenerateDiffWithOptions(current, desired *unstructured.Unstructured, _ DiffOptions) (*ResourceDiff, error) {
	var diffType DiffType

	// Determine diff type
	switch {
	case current == nil && desired != nil:
		diffType = DiffTypeAdded
	case current != nil && desired == nil:
		diffType = DiffTypeRemoved
	case current != nil: // && desired != nil:
		diffType = DiffTypeModified
	default:
		return nil, errors.New("both current and desired cannot be nil")
	}

	// For modifications, check if objects are semantically equal
	if diffType == DiffTypeModified {
		if equality.Semantic.DeepEqual(current, desired) {
			// Objects are completely equal, return nil to indicate no diff
			return equalDiff(current, desired), nil
		}

		// Clean up both objects for comparison
		currentClean := cleanupForDiff(current.DeepCopy())
		desiredClean := cleanupForDiff(desired.DeepCopy())

		// Check if the cleaned objects are equal
		if equality.Semantic.DeepEqual(currentClean.Object, desiredClean.Object) {
			// Objects are equal in terms of the content we care about,
			// but may have metadata differences - return DiffTypeEqual
			return equalDiff(current, desired), nil
		}
	}

	asString := func(obj *unstructured.Unstructured) (string, error) {
		if obj == nil {
			return "", nil
		}
		clean := cleanupForDiff(obj.DeepCopy())
		yaml, err := sigsyaml.Marshal(clean.Object)
		if err != nil {
			return "", err
		}
		return string(yaml), nil
	}

	currentStr, err := asString(current)
	if err != nil {
		return nil, errors.Wrap(err, "cannot marshal current object to YAML")
	}

	desiredStr, err := asString(desired)
	if err != nil {
		return nil, errors.Wrap(err, "cannot marshal desired object to YAML")
	}

	// Return nil if content is identical
	if desiredStr == currentStr {
		return equalDiff(current, desired), nil
	}

	// Get the line by line diff
	lineDiffs := GetLineDiff(currentStr, desiredStr)

	if len(lineDiffs) == 0 {
		return equalDiff(current, desired), nil
	}

	// Extract resource kind and name
	var kind, name string
	if desired != nil {
		kind = desired.GetKind()
		name = desired.GetName()
	} else {
		kind = current.GetKind()
		name = current.GetName()
	}

	return &ResourceDiff{
		ResourceKind: kind,
		ResourceName: name,
		DiffType:     diffType,
		LineDiffs:    lineDiffs,
		Current:      current,
		Desired:      desired,
	}, nil
}

func equalDiff(current *unstructured.Unstructured, desired *unstructured.Unstructured) *ResourceDiff {
	return &ResourceDiff{
		ResourceKind: current.GetKind(),
		ResourceName: current.GetName(),
		DiffType:     DiffTypeEqual,
		LineDiffs:    []diffmatchpatch.Diff{},
		Current:      current,
		Desired:      desired,
	}
}

// processLines extracts lines from a diff and processes them into a standardized format
// Returns the processed lines and whether there was a trailing newline
func processLines(diff diffmatchpatch.Diff, options DiffOptions) ([]string, bool) {
	lines := strings.Split(diff.Text, "\n")
	hasTrailingNewline := strings.HasSuffix(diff.Text, "\n")

	// If there's a trailing newline, the split produces an empty string at the end
	if hasTrailingNewline && len(lines) > 0 {
		lines = lines[:len(lines)-1]
	}

	var result []string

	// Format each line with appropriate prefix and color
	for _, line := range lines {
		result = append(result, formatLine(line, diff.Type, options))
	}

	// Add formatted empty line if there was just a newline
	if hasTrailingNewline && len(lines) == 0 {
		result = append(result, formatLine("", diff.Type, options))
	}

	return result, hasTrailingNewline
}

// formatLine applies the appropriate prefix and color to a single line
func formatLine(line string, diffType diffmatchpatch.Operation, options DiffOptions) string {
	var prefix string
	var colorStart, colorEnd string

	switch diffType {
	case diffmatchpatch.DiffInsert:
		prefix = options.AddPrefix
		if options.UseColors {
			colorStart = ColorGreen
			colorEnd = ColorReset
		}
	case diffmatchpatch.DiffDelete:
		prefix = options.DeletePrefix
		if options.UseColors {
			colorStart = ColorRed
			colorEnd = ColorReset
		}
	case diffmatchpatch.DiffEqual:
		prefix = options.ContextPrefix
	}

	if options.UseColors && colorStart != "" {
		return fmt.Sprintf("%s%s%s%s", colorStart, prefix, line, colorEnd)
	}
	return fmt.Sprintf("%s%s", prefix, line)
}

// cleanupForDiff removes fields that shouldn't be included in the diff
func cleanupForDiff(obj *unstructured.Unstructured) *unstructured.Unstructured {
	// Remove server-side fields and metadata that we don't want to diff
	metadata, found, _ := unstructured.NestedMap(obj.Object, "metadata")
	if found {
		// Remove fields that change automatically or are server-side
		fieldsToRemove := []string{
			"resourceVersion",
			"uid",
			"generation",
			"creationTimestamp",
			"managedFields",
			"selfLink",
			"ownerReferences",
		}

		for _, field := range fieldsToRemove {
			delete(metadata, field)
		}

		unstructured.SetNestedMap(obj.Object, metadata, "metadata")
	}

	// Remove resourceRefs field from spec if it exists
	// This ensures it doesn't affect diff calculations
	spec, found, _ := unstructured.NestedMap(obj.Object, "spec")
	if found && spec != nil {
		delete(spec, "resourceRefs")
		unstructured.SetNestedMap(obj.Object, spec, "spec")
	}

	// Remove status field as we're focused on spec changes
	delete(obj.Object, "status")

	return obj
}
