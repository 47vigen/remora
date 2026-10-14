// Derived from maximhq/bifrost ui/lib/constants/logs.ts (Apache-2.0); modified.
import type { ProviderIconType } from "../components/provider-icon/icons";

// Known provider names array - centralized definition
export const KnownProvidersNames = [
  "anthropic",
  "azure",
  "bedrock",
  "bedrock_mantle",
  "cerebras",
  "cohere",
  "deepseek",
  "gemini",
  "groq",
  "huggingface",
  "mistral",
  "ollama",
  "opencode-go",
  "opencode-zen",
  "openai",
  "openrouter",
  "parasail",
  "elevenlabs",
  "perplexity",
  "sgl",
  "vertex",
  "nebius",
  "xai",
  "replicate",
  "vllm",
  "runway",
  "runware",
  "fireworks",
  "sarvam",
  "wafer",
  "databricks",
  "cloudflare",
  "github-copilot",
  "typesafe",
] as const;

// Local Provider type derived from KNOWN_PROVIDERS constant
export type ProviderName = (typeof KnownProvidersNames)[number];

export const Statuses = ["success", "error", "processing", "cancelled"] as const;

export const RequestTypes = [
  "list_models",
  "model_retrieve",
  "text_completion",
  "text_completion_stream",
  "chat_completion",
  "chat_completion_stream",
  "responses",
  "responses_stream",
  "responses_retrieve",
  "responses_retrieve_stream",
  "responses_delete",
  "responses_cancel",
  "responses_input_items",
  "embedding",
  "rerank",
  "decisions",
  "speech",
  "speech_stream",
  "transcription",
  "transcription_stream",
  "image_generation",
  "image_generation_stream",
  "image_edit",
  "image_edit_stream",
  "image_variation",
  "ocr",
  "ocr_stream",
  "video_generation",
  "video_edit",
  "video_retrieve",
  "video_download",
  "video_delete",
  "video_list",
  "video_remix",
  "count_tokens",
  "compaction",
  // Batch operations
  "batch_create",
  "batch_list",
  "batch_retrieve",
  "batch_cancel",
  "batch_delete",
  "batch_results",
  // File operations
  "file_upload",
  "file_list",
  "file_retrieve",
  "file_delete",
  "file_content",
  // Container operations
  "container_create",
  "container_list",
  "container_retrieve",
  "container_delete",
  // Container file operations
  "container_file_create",
  "container_file_list",
  "container_file_retrieve",
  "container_file_content",
  "container_file_delete",
  "passthrough",
  "passthrough_stream",
  // WebSocket/Realtime operations
  "websocket_responses",
  "realtime",
  "realtime.turn",
  // GPT Live: one row per session, delegations inside it; a recording download is its own row
  "live",
  "live.session",
  "live_content",
  "live_sideband",
] as const;

export const ProviderLabels: Record<ProviderName, string> = {
  openai: "OpenAI",
  anthropic: "Anthropic",
  azure: "Azure",
  bedrock: "AWS Bedrock",
  bedrock_mantle: "AWS Bedrock Mantle",
  cohere: "Cohere",
  deepseek: "DeepSeek",
  vertex: "Vertex AI",
  mistral: "Mistral AI",
  ollama: "Ollama",
  "opencode-go": "OpenCode Go",
  "opencode-zen": "OpenCode Zen",
  groq: "Groq",
  parasail: "Parasail",
  elevenlabs: "Elevenlabs",
  perplexity: "Perplexity",
  sgl: "SGLang",
  cerebras: "Cerebras",
  gemini: "Gemini",
  openrouter: "OpenRouter",
  huggingface: "HuggingFace",
  nebius: "Nebius Token Factory",
  xai: "xAI",
  replicate: "Replicate",
  vllm: "vLLM",
  runway: "Runway",
  runware: "Runware",
  fireworks: "Fireworks AI",
  sarvam: "Sarvam AI",
  wafer: "Wafer",
  databricks: "Databricks",
  cloudflare: "Cloudflare",
  "github-copilot": "GitHub Copilot",
  typesafe: "TypeSafe",
} as const;

// Helper function to get provider label, supporting custom providers
export const getProviderLabel = (provider: string): string => {
  // Use hasOwnProperty for safe lookup without checking prototype chain
  if (Object.prototype.hasOwnProperty.call(ProviderLabels, provider.toLowerCase().trim() as ProviderName)) {
    return ProviderLabels[provider.toLowerCase().trim() as ProviderName];
  }

  // For custom providers, return the original provider name as is
  return provider;
};

export const RequestTypeLabels = {
  "chat.completion": "Chat",
  response: "Responses",
  "response.completion.chunk": "Responses Stream",
  completion: "Completion",
  "text.completion": "Text",
  list: "List",
  "audio.speech": "Speech",
  "audio.transcription": "Transcription",
  "chat.completion.chunk": "Chat Stream",
  "audio.speech.chunk": "Speech Stream",
  "audio.transcription.chunk": "Transcription Stream",

  // Request Types
  list_models: "List Models",
  model_retrieve: "Retrieve Model",
  text_completion: "Text",
  text_completion_stream: "Text Stream",
  chat_completion: "Chat",
  chat_completion_stream: "Chat Stream",
  responses: "Responses",
  responses_stream: "Responses Stream",
  responses_retrieve: "Responses Retrieve",
  responses_retrieve_stream: "Responses Retrieve Stream",
  responses_delete: "Responses Delete",
  responses_cancel: "Responses Cancel",
  responses_input_items: "Responses Input Items",

  embedding: "Embedding",
  rerank: "Rerank",
  decisions: "Decisions",

  speech: "Speech",
  speech_stream: "Speech Stream",

  transcription: "Transcription",
  transcription_stream: "Transcription Stream",

  image_generation: "Image Generation",
  image_generation_stream: "Image Generation Stream",
  image_edit: "Image Edit",
  image_edit_stream: "Image Edit Stream",
  image_variation: "Image Variation",
  ocr: "OCR",
  ocr_stream: "OCR Stream",
  video_generation: "Video Generation",
  video_edit: "Video Edit",
  video_retrieve: "Video Retrieve",
  video_download: "Video Download",
  video_delete: "Video Delete",
  video_list: "Video List",
  video_remix: "Video Remix",
  count_tokens: "Count Tokens",
  compaction: "Compaction",

  batch_create: "Batch Create",
  batch_list: "Batch List",
  batch_retrieve: "Batch Retrieve",
  batch_cancel: "Batch Cancel",
  batch_delete: "Batch Delete",
  batch_results: "Batch Results",

  file_upload: "File Upload",
  file_list: "File List",
  file_retrieve: "File Retrieve",
  file_delete: "File Delete",
  file_content: "File Content",

  // Container operations
  container_create: "Container Create",
  container_list: "Container List",
  container_retrieve: "Container Retrieve",
  container_delete: "Container Delete",

  // Container file operations
  container_file_create: "Container File Create",
  container_file_list: "Container File List",
  container_file_retrieve: "Container File Retrieve",
  container_file_content: "Container File Content",
  container_file_delete: "Container File Delete",

  passthrough: "Passthrough",
  passthrough_stream: "Passthrough Stream",
  // WebSocket operations
  websocket_responses: "WebSocket Responses",
  realtime: "Realtime",
  "realtime.turn": "Realtime Turn",
  live: "Live",
  "live.session": "Live Session",
  live_content: "Live Recording",
  live_sideband: "Live Sideband",
} as const;

export type Status = (typeof Statuses)[number];

// Subscription providers, by the names users see. Accounts for these sign in through OAuth; they are not API-key
// providers and have no form of their own.
export const OAUTH_PROVIDERS = ["antigravity", "claude", "codex", "devin", "grok", "kimi", "kimi-ai", "meta"] as const;

export type OAuthProviderName = (typeof OAUTH_PROVIDERS)[number];

export const OAuthProviderLabels: Record<OAuthProviderName, string> = {
  antigravity: "Antigravity",
  claude: "Claude",
  codex: "Codex",
  devin: "Devin",
  grok: "Grok",
  kimi: "Kimi",
  "kimi-ai": "Kimi AI",
  meta: "Meta",
};

// The icon each subscription provider borrows. The value is an icon key and is never shown.
export const ICON_FOR_OAUTH: Record<string, ProviderIconType> = {
  codex: "openai",
  claude: "anthropic",
  antigravity: "gemini",
  grok: "xai",
};

// Reports whether a provider name is a subscription provider.
export const isOAuthProvider = (name: string): name is OAuthProviderName =>
  (OAUTH_PROVIDERS as readonly string[]).includes(name);

// The label of a subscription provider, or the name itself when it is not one.
export const getOAuthProviderLabel = (name: string): string =>
  isOAuthProvider(name) ? OAuthProviderLabels[name] : name;
