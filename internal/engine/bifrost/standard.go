package bifrost

// OAuthProvider is the OAuth engine's front-door provider (plan 03); its routing rules are named "remora-oauth-<name>".
const OAuthProvider = "remora-oauth"

// SonioxProvider is the custom provider for the speech adapter; the public name is now a built-in provider.
const (
	SonioxProvider   = "remora-soniox"
	SonioxPublicName = "soniox"
)

// PublicProvider returns the public name of the speech adapter's engine provider.
func PublicProvider(name string) string {
	if name == SonioxProvider {
		return SonioxPublicName
	}
	return name
}

// standardProviders copies the engine's built-in provider list (core/schemas/bifrost.go, StandardProviders). A custom
// provider must not take one of these names. The end-to-end suite catches drift when the engine is bumped.
var standardProviders = map[string]struct{}{
	"anthropic":      {},
	"azure":          {},
	"bedrock":        {},
	"bedrock_mantle": {},
	"cerebras":       {},
	"cohere":         {},
	"deepseek":       {},
	"gemini":         {},
	"groq":           {},
	"mistral":        {},
	"ollama":         {},
	"opencode-go":    {},
	"opencode-zen":   {},
	"openai":         {},
	"parasail":       {},
	"perplexity":     {},
	"sgl":            {},
	"vertex":         {},
	"openrouter":     {},
	"elevenlabs":     {},
	"huggingface":    {},
	"nebius":         {},
	"xai":            {},
	"replicate":      {},
	"vllm":           {},
	"runway":         {},
	"runware":        {},
	"fireworks":      {},
	"sarvam":         {},
	"wafer":          {},
	"github-copilot": {},
	"databricks":     {},
	"cloudflare":     {},
	"typesafe":       {},
	"sagemaker":      {},
	"minimax":        {},
	"byteplus":       {},
	"machgen":        {},
	"baseten":        {},
	"kimchi":         {},
	SonioxPublicName: {},
}

// IsStandardProvider reports whether name is one of the engine's built-in providers.
func IsStandardProvider(name string) bool {
	_, ok := standardProviders[name]
	return ok
}
