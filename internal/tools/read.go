package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/oschina/mothx/internal/imageproc"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/util"
)

// ReadTool reads file contents.
type ReadTool struct {
	registry *Registry
}

// NewReadTool creates a new read tool.
func NewReadTool(r *Registry) *ReadTool {
	return &ReadTool{registry: r}
}

func (t *ReadTool) Name() string { return "read" }

func (t *ReadTool) Description() string {
	return "Read the contents of a file. Supports text files and images (jpg, png, gif, webp). For text files, output is truncated at 2000 lines or 50KB. Use offset/limit for large files. For images, use imageMode=detail when OCR, screenshots, diagrams, or small text require more visual detail."
}

func (t *ReadTool) PromptSnippet() string {
	return "Read file contents (preferred for inspecting files)"
}

func (t *ReadTool) PromptGuidelines() []string {
	return []string{
		"Use read to examine files instead of cat or sed.",
		"For image OCR, screenshots, diagrams, or small UI text, use read with imageMode=\"detail\".",
		"For localized image questions, use the crop parameter in source image pixels before reading the full image at high detail.",
	}
}

func (t *ReadTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "Path to the file to read"
			},
			"offset": {
				"type": "integer",
				"description": "Line number to start reading from (1-indexed)"
			},
			"limit": {
				"type": "integer",
				"description": "Maximum number of lines to read"
			},
			"imageMode": {
				"type": "string",
				"enum": ["auto", "fast", "detail", "raw"],
				"description": "Image processing mode for image files. auto balances quality and request size; fast uses lower resolution; detail preserves more detail; raw sends the original file after safety checks."
			},
			"maxLongEdge": {
				"type": "integer",
				"description": "Optional maximum long edge in pixels for image resizing"
			},
			"crop": {
				"type": "object",
				"description": "Optional crop rectangle in source image pixels before resizing",
				"properties": {
					"x": {"type": "integer"},
					"y": {"type": "integer"},
					"width": {"type": "integer"},
					"height": {"type": "integer"}
				},
				"required": ["x", "y", "width", "height"]
			}
		},
		"required": ["path"]
	}`)
}

// imageMimeType maps file extensions to MIME types.
var imageMimeType = map[string]string{
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".gif":  "image/gif",
	".webp": "image/webp",
}

func (t *ReadTool) Execute(ctx context.Context, params map[string]any) (ToolResult, error) {
	path, _ := params["path"].(string)
	if path == "" {
		return ToolResult{}, fmt.Errorf("path is required")
	}

	path, err := t.registry.ResolvePath(path)
	if err != nil {
		return ToolResult{}, fmt.Errorf("invalid path: %w", err)
	}

	// Check for image files
	ext := strings.ToLower(filepath.Ext(path))
	if mimeType, ok := imageMimeType[ext]; ok {
		policy, err := t.imageReadPolicy(params)
		if err != nil {
			return ToolResult{}, err
		}
		info, err := os.Stat(path)
		if err != nil {
			return ToolResult{}, fmt.Errorf("cannot stat image file: %w", err)
		}
		if policy.MaxFileBytes > 0 && info.Size() > policy.MaxFileBytes {
			return ToolResult{}, fmt.Errorf("image file too large: %d bytes (max %d)", info.Size(), policy.MaxFileBytes)
		}
		result, err := imageproc.PrepareFile(path, policy)
		if err != nil {
			return ToolResult{}, fmt.Errorf("cannot read image file: %w", err)
		}
		image := provider.ImageContent{
			Data:           base64.StdEncoding.EncodeToString(result.Data),
			MimeType:       result.MimeType,
			Width:          result.Meta.Width,
			Height:         result.Meta.Height,
			Bytes:          result.Meta.Bytes,
			OriginalWidth:  result.Meta.OriginalWidth,
			OriginalHeight: result.Meta.OriginalHeight,
			OriginalBytes:  result.Meta.OriginalBytes,
			Detail:         result.Meta.Detail,
			Scale:          result.Meta.Scale,
			Cropped:        result.Meta.Cropped,
			CropX:          result.Meta.CropX,
			CropY:          result.Meta.CropY,
			CropWidth:      result.Meta.CropWidth,
			CropHeight:     result.Meta.CropHeight,
		}
		desc := imageDescription(path, mimeType, result)
		return NewImageToolResultWithContent(desc, image), nil
	}

	// Read text file
	data, err := os.ReadFile(path)
	if err != nil {
		return ToolResult{}, fmt.Errorf("cannot read file: %w", err)
	}

	content := string(data)
	lines := strings.Split(content, "\n")

	offset := 0
	if v, ok := params["offset"].(float64); ok && v > 0 {
		offset = int(v) - 1 // Convert to 0-indexed
	}

	limit := len(lines)
	if v, ok := params["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}

	// Clamp
	if offset >= len(lines) {
		return NewTextToolResult("(end of file)"), nil
	}
	end := offset + limit
	if end > len(lines) {
		end = len(lines)
	}

	selected := lines[offset:end]

	// Number lines
	var sb strings.Builder
	for i, line := range selected {
		lineNum := offset + i + 1
		sb.WriteString(fmt.Sprintf("%d\t%s\n", lineNum, line))
	}

	result := sb.String()

	// Truncate
	const maxBytes = 50000
	if len(result) > maxBytes {
		result = util.TruncateString(result, maxBytes) + fmt.Sprintf("\n... (truncated, total %d lines)", len(lines))
	}

	return NewTextToolResult(result), nil
}

func (t *ReadTool) imageReadPolicy(params map[string]any) (imageproc.Policy, error) {
	mode := imageproc.NormalizeMode("")
	if v, ok := params["imageMode"].(string); ok {
		mode = imageproc.NormalizeMode(v)
	}
	policy := t.registry.ImagePolicy(mode)
	if v, ok := params["maxLongEdge"].(float64); ok && v > 0 {
		policy.MaxLongEdge = int(v)
	} else if v, ok := params["maxLongEdge"].(int); ok && v > 0 {
		policy.MaxLongEdge = v
	}
	if crop, ok, err := imageCropParam(params["crop"]); err != nil {
		return imageproc.Policy{}, err
	} else if ok {
		policy.Crop = crop
	}
	return policy, nil
}

func imageDescription(path, sourceMime string, result imageproc.Result) string {
	original := fmt.Sprintf("%dx%d %s %s", result.Meta.OriginalWidth, result.Meta.OriginalHeight, formatBytes(result.Meta.OriginalBytes), sourceMime)
	sent := fmt.Sprintf("%dx%d %s %s", result.Meta.Width, result.Meta.Height, formatBytes(result.Meta.Bytes), result.MimeType)
	crop := ""
	if result.Meta.Cropped {
		crop = fmt.Sprintf(", crop: %dx%d+%d+%d", result.Meta.CropWidth, result.Meta.CropHeight, result.Meta.CropX, result.Meta.CropY)
	}
	if result.Meta.Resized || result.Meta.Transcoded || result.Meta.OriginalBytes != result.Meta.Bytes || sourceMime != result.MimeType {
		return fmt.Sprintf("[Image file: %s, original: %s%s, sent: %s, mode: %s]", path, original, crop, sent, result.Meta.Detail)
	}
	return fmt.Sprintf("[Image file: %s, %s%s, mode: %s]", path, sent, crop, result.Meta.Detail)
}

func imageCropParam(value any) (*imageproc.Crop, bool, error) {
	if value == nil {
		return nil, false, nil
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return nil, false, fmt.Errorf("crop must be an object")
	}
	crop := &imageproc.Crop{
		X:      intParamValue(obj["x"]),
		Y:      intParamValue(obj["y"]),
		Width:  intParamValue(obj["width"]),
		Height: intParamValue(obj["height"]),
	}
	return crop, true, nil
}

func intParamValue(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		i, _ := v.Int64()
		return int(i)
	default:
		return 0
	}
}

func formatBytes(n int) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	kb := float64(n) / unit
	if kb < unit {
		return fmt.Sprintf("%.1fKB", kb)
	}
	return fmt.Sprintf("%.1fMB", kb/unit)
}
