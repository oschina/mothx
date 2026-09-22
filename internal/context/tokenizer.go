package context

import (
	"strings"

	"github.com/oschina/mothx/internal/imageproc"
	"github.com/oschina/mothx/internal/provider"
)

// TokenEstimator estimates the context footprint of provider messages.
type TokenEstimator interface {
	EstimateTokens(msg provider.Message) int
	EstimateMessagesTokens(messages []provider.Message) int
}

// GenericTokenEstimator uses the embedded DeepSeek V3 byte-level BPE
// tokenizer for text and the existing provider-aware formulas for images.
type GenericTokenEstimator struct{}

func (GenericTokenEstimator) EstimateTokens(msg provider.Message) int {
	return estimateMessageTokens(msg, estimateImageTokens)
}

func (e GenericTokenEstimator) EstimateMessagesTokens(messages []provider.Message) int {
	total := 0
	for _, msg := range messages {
		total += e.EstimateTokens(msg)
	}
	return total
}

// ModelAwareTokenEstimator uses the same local DeepSeek V3 tokenizer for all
// text. Model-specific image formulas are retained because image tokens are
// not represented by the text tokenizer.
type ModelAwareTokenEstimator struct {
	Model *provider.Model
}

func (e ModelAwareTokenEstimator) EstimateTokens(msg provider.Message) int {
	return estimateMessageTokens(msg, func(image *provider.ImageContent) int {
		return estimateImageTokensForModelOrGeneric(image, e.Model)
	})
}

func (e ModelAwareTokenEstimator) EstimateMessagesTokens(messages []provider.Message) int {
	total := 0
	for _, msg := range messages {
		total += e.EstimateTokens(msg)
	}
	return total
}

func estimateMessageTokens(msg provider.Message, estimateImage func(*provider.ImageContent) int) int {
	tokens := 0
	if len(msg.Contents) > 0 {
		for _, block := range msg.Contents {
			switch block.Type {
			case "text":
				tokens += deepSeekTokenCount(block.Text)
			case "thinking":
				tokens += deepSeekTokenCount(block.Thinking)
			case "toolCall":
				if block.ToolCall != nil {
					tokens += deepSeekTokenCount(block.ToolCall.Name)
					tokens += deepSeekTokenCount(string(block.ToolCall.Arguments))
				}
			case "image":
				tokens += estimateImage(block.Image)
			}
		}
		return tokens
	}
	return deepSeekTokenCount(msg.Content)
}

func estimateImageTokens(image *provider.ImageContent) int {
	if image != nil && image.Width > 0 && image.Height > 0 {
		return estimateGenericImageTokens(image.Width, image.Height)
	}
	// Preserve the previous minimum visual-token cost and payload-size guard.
	imageChars := 4800
	if image != nil && len(image.Data) > imageChars {
		imageChars = len(image.Data)
	}
	return (imageChars + 3) / 4
}

func estimateImageTokensForModelOrGeneric(image *provider.ImageContent, model *provider.Model) int {
	if tokens := estimateImageTokensForModel(image, model); tokens > 0 {
		return tokens
	}
	return estimateImageTokens(image)
}

// ResolveTokenEstimator returns the configured estimator. Text always uses the
// embedded DeepSeek V3 tokenizer; the model only affects image accounting.
func ResolveTokenEstimator(settings CompactionSettings, model *provider.Model) TokenEstimator {
	_ = settings.Tokenizer // retained for config compatibility
	if model != nil {
		return ModelAwareTokenEstimator{Model: model}
	}
	return GenericTokenEstimator{}
}

// EstimateTextTokens returns the shared local text token estimate.
func EstimateTextTokens(text string) int {
	return deepSeekTokenCount(text)
}

// EstimateGuardTokens returns a conservative request-size estimate for one
// message. It uses the shared tokenizer, with a payload-size floor for tool
// results whose repetitive content can compress unusually well under BPE.
func EstimateGuardTokens(msg provider.Message, estimator TokenEstimator) int {
	if estimator == nil {
		estimator = GenericTokenEstimator{}
	}
	tokens := estimator.EstimateTokens(msg)
	if msg.Role != "toolResult" {
		return tokens
	}
	floor := (estimateMessageChars(msg) + 3) / 4
	if floor > tokens {
		return floor
	}
	return tokens
}

func estimateMessageChars(msg provider.Message) int {
	return estimateMessageCharsWithImageEstimator(msg, estimateImageChars)
}

func estimateMessageCharsWithImageEstimator(msg provider.Message, estimateImage func(*provider.ImageContent) int) int {
	chars := 0

	if len(msg.Contents) > 0 {
		// Rich content blocks take precedence; avoid double-counting with Content.
		for _, block := range msg.Contents {
			switch block.Type {
			case "text":
				chars += len(block.Text)
			case "thinking":
				chars += len(block.Thinking)
			case "toolCall":
				if block.ToolCall != nil {
					chars += len(block.ToolCall.Name)
					chars += len(block.ToolCall.Arguments)
				}
			case "image":
				chars += estimateImage(block.Image)
			}
		}
	} else if msg.Content != "" {
		chars += len(msg.Content)
	}

	return chars
}

func estimateImageChars(image *provider.ImageContent) int {
	if image != nil && image.Width > 0 && image.Height > 0 {
		tokens := estimateGenericImageTokens(image.Width, image.Height)
		return tokens * 4
	}
	// Preserve the existing minimum visual-token cost and payload-size guard.
	// Provider-specific image estimators can replace this through TokenEstimator
	// without changing callers.
	imageChars := 4800
	if image != nil && len(image.Data) > imageChars {
		imageChars = len(image.Data)
	}
	return imageChars
}

func estimateGenericImageTokens(width, height int) int {
	return ceilDiv(width, 512) * ceilDiv(height, 512) * 800
}

func estimateImageTokensForModel(image *provider.ImageContent, model *provider.Model) int {
	if image == nil || image.Width <= 0 || image.Height <= 0 || model == nil {
		return 0
	}
	family := imageproc.InferFamily(imageproc.Hint{
		ProviderID: model.Provider,
		ModelID:    model.ID,
	})
	switch family {
	case imageproc.FamilyAnthropic, imageproc.FamilyAnthropicBedrock:
		return estimatePatchImageTokens(image.Width, image.Height, 28)
	case imageproc.FamilyGemini:
		return estimateGeminiImageTokens(image.Width, image.Height)
	case imageproc.FamilyQwen:
		return estimatePatchImageTokens(image.Width, image.Height, 28)
	case imageproc.FamilyOpenAI, imageproc.FamilyGrok:
		return estimateOpenAIImageTokens(image)
	default:
		return 0
	}
}

func estimatePatchImageTokens(width, height, patch int) int {
	return ceilDiv(width, patch) * ceilDiv(height, patch)
}

func estimateGeminiImageTokens(width, height int) int {
	if width <= 384 && height <= 384 {
		return 258
	}
	return ceilDiv(width, 768) * ceilDiv(height, 768) * 258
}

func estimateOpenAIImageTokens(image *provider.ImageContent) int {
	switch strings.ToLower(strings.TrimSpace(image.Detail)) {
	case "fast", "low":
		return 85
	default:
		tiles := ceilDiv(image.Width, 512) * ceilDiv(image.Height, 512)
		return 85 + 170*tiles
	}
}

func ceilDiv(n, d int) int {
	if d <= 0 {
		return 0
	}
	return (n + d - 1) / d
}
