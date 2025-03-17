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

// GenerateDiff produces a formatted diff between two unstructured objects
func GenerateDiff(current, desired *unstructured.Unstructured, kind, name string) (string, error) {

	// If the objects are equal, return an empty diff
	if equality.Semantic.DeepEqual(current, desired) {
		return "", nil
	}

	cleanAndRender := func(obj *unstructured.Unstructured) (string, error) {
		clean := cleanupForDiff(obj.DeepCopy())

		// Convert both objects to YAML strings for diffing
		cleanYAML, err := sigsyaml.Marshal(clean.Object)
		if err != nil {
			return "", errors.Wrap(err, "cannot marshal current object to YAML")
		}

		return string(cleanYAML), nil
	}

	currentStr := ""
	var err error
	if current != nil {
		currentStr, err = cleanAndRender(current)
		if err != nil {
			return "", err
		}
	}

	desiredStr, err := cleanAndRender(desired)

	// Return an empty diff
	if desiredStr == currentStr {
		return "", nil
	}

	// get the full line by line diff
	diffResult := GetLineDiff(currentStr, desiredStr, DefaultDiffOptions())

	if diffResult == "" {
		return "", nil
	}

	var leadChar string

	switch current {
	case nil:
		leadChar = "+++" // Resource does not exist
		// TODO: deleted resources should be shown as deleted
	default:
		leadChar = "~~~" // Resource exists and is changing
	}

	// Format the output with a resource header
	return fmt.Sprintf("%s %s/%s\n%s", leadChar, kind, name, diffResult), nil
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

	// Remove status field as we're focused on spec changes
	delete(obj.Object, "status")

	return obj
}

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

// Format implements the DiffFormatter interface for FullDiffFormatter
func (f *FullDiffFormatter) Format(diffs []diffmatchpatch.Diff, options DiffOptions) string {
	var builder strings.Builder

	// Set color variables based on options
	addColor := ""
	delColor := ""
	resetColor := ""
	if options.UseColors {
		addColor = ColorGreen
		delColor = ColorRed
		resetColor = ColorReset
	}

	for _, diff := range diffs {
		lines := strings.Split(diff.Text, "\n")

		// Handle the trailing newline correctly
		hasTrailingNewline := strings.HasSuffix(diff.Text, "\n")
		if hasTrailingNewline && len(lines) > 0 {
			lines = lines[:len(lines)-1]
		}

		switch diff.Type {
		case diffmatchpatch.DiffInsert:
			for _, line := range lines {
				builder.WriteString(fmt.Sprintf("%s%s%s%s\n", addColor, options.AddPrefix, line, resetColor))
			}
			if hasTrailingNewline && len(lines) == 0 {
				builder.WriteString(fmt.Sprintf("%s%s%s\n", addColor, options.AddPrefix, resetColor))
			}

		case diffmatchpatch.DiffDelete:
			for _, line := range lines {
				builder.WriteString(fmt.Sprintf("%s%s%s%s\n", delColor, options.DeletePrefix, line, resetColor))
			}
			if hasTrailingNewline && len(lines) == 0 {
				builder.WriteString(fmt.Sprintf("%s%s%s\n", delColor, options.DeletePrefix, resetColor))
			}

		case diffmatchpatch.DiffEqual:
			for _, line := range lines {
				builder.WriteString(fmt.Sprintf("%s%s\n", options.ContextPrefix, line))
			}
			if hasTrailingNewline && len(lines) == 0 {
				builder.WriteString(fmt.Sprintf("%s\n", options.ContextPrefix))
			}
		}
	}

	return builder.String()
}

// Format implements the DiffFormatter interface for CompactDiffFormatter
// Format implements the DiffFormatter interface for CompactDiffFormatter
func (f *CompactDiffFormatter) Format(diffs []diffmatchpatch.Diff, options DiffOptions) string {
	// Set color variables based on options
	addColor := ""
	delColor := ""
	resetColor := ""
	if options.UseColors {
		addColor = ColorGreen
		delColor = ColorRed
		resetColor = ColorReset
	}

	// First, convert diffs to line-based items
	type lineItem struct {
		Type    diffmatchpatch.Operation
		Content string
	}

	var allLines []lineItem

	// Process all diffs into individual lines
	for _, diff := range diffs {
		lines := strings.Split(diff.Text, "\n")

		// Handle the trailing newline correctly
		hasTrailingNewline := strings.HasSuffix(diff.Text, "\n")
		if hasTrailingNewline && len(lines) > 0 {
			lines = lines[:len(lines)-1]
		}

		for _, line := range lines {
			allLines = append(allLines, lineItem{
				Type:    diff.Type,
				Content: line,
			})
		}

		// Add an empty line for trailing newlines
		if hasTrailingNewline && len(lines) == 0 {
			allLines = append(allLines, lineItem{
				Type:    diff.Type,
				Content: "",
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

	// If we have no change blocks, just return empty string
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
				builder.WriteString(fmt.Sprintf("%s%s\n", options.ContextPrefix, allLines[i].Content))
				lastPrintedIdx = i
			}
		}

		// Print the changes
		for i := block.StartIdx; i <= block.EndIdx; i++ {
			switch allLines[i].Type {
			case diffmatchpatch.DiffInsert:
				builder.WriteString(fmt.Sprintf("%s%s%s%s\n", addColor, options.AddPrefix, allLines[i].Content, resetColor))
			case diffmatchpatch.DiffDelete:
				builder.WriteString(fmt.Sprintf("%s%s%s%s\n", delColor, options.DeletePrefix, allLines[i].Content, resetColor))
			}
			lastPrintedIdx = i
		}

		// Print context after the change
		contextEnd := min(len(allLines), block.EndIdx+contextLines+1)
		for i := block.EndIdx + 1; i < contextEnd; i++ {
			builder.WriteString(fmt.Sprintf("%s%s\n", options.ContextPrefix, allLines[i].Content))
			lastPrintedIdx = i
		}
	}

	return builder.String()
}

// GetLineDiff performs a proper line-by-line diff and returns the formatted result
func GetLineDiff(oldText, newText string, options DiffOptions) string {
	patch := diffmatchpatch.New()

	// Use the line-to-char conversion to treat each line as an atomic unit
	ch1, ch2, lines := patch.DiffLinesToChars(oldText, newText)

	diff := patch.DiffMain(ch1, ch2, false)
	patch.DiffCleanupSemantic(diff)

	lineDiffs := patch.DiffCharsToLines(diff, lines)

	// Use the appropriate formatter
	formatter := NewFormatter(options.Compact)
	return formatter.Format(lineDiffs, options)
}
