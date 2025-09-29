package main

// facadeSearchHit represents a deterministic example search hit surfaced during
// ChatGPT connector verification. These entries should mirror real documents so
// the verifier can fetch follow-up content without depending on upstream
// indexes.
type facadeSearchHit struct {
	ID      string
	Title   string
	Text    string
	URL     string
	Snippet string
}

var defaultFacadeSearchHits = []facadeSearchHit{
	{
		ID:    "repo:docs/SPEC-v1.md",
		Title: "SPEC-v1.md",
		Text:  "Summary of the Stelae MCP compliance requirements and verification flow.",
		URL:   "stelae://repo/docs/SPEC-v1.md",
		Snippet: "SPEC outlines the MCP handshake contract, tool catalog expectations, " +
			"and SSE timing guarantees.",
	},
	{
		ID:      "repo:dev/chat_gpt_connector_compliant_reference.md",
		Title:   "chat_gpt_connector_compliant_reference.md",
		Text:    "Reference catalog consolidating manifest, initialize, and search requirements for ChatGPT connectors.",
		URL:     "stelae://repo/dev/chat_gpt_connector_compliant_reference.md",
		Snippet: "Reference doc captures the minimal search/fetch tool set plus example payloads used by compliant servers.",
	},
	{
		ID:      "repo:dev/compliance_handoff.md",
		Title:   "compliance_handoff.md",
		Text:    "Action plan enumerating the remediation steps to align the Stelae MCP endpoint with ChatGPT verification.",
		URL:     "stelae://repo/dev/compliance_handoff.md",
		Snippet: "Handoff describes trimming initialize/tools.list outputs and delivering deterministic search hits for validation.",
	},
}

func buildFacadeSearchPayload(_ string) map[string]any {
	results := make([]map[string]any, 0, len(defaultFacadeSearchHits))
	for _, hit := range defaultFacadeSearchHits {
		results = append(results, map[string]any{
			"id":    hit.ID,
			"title": hit.Title,
			"text":  hit.Text,
			"url":   hit.URL,
			"metadata": map[string]any{
				"snippet": hit.Snippet,
			},
		})
	}
	return map[string]any{"results": results}
}

func buildFacadeFetchPayload(id string) (map[string]any, bool) {
	for _, hit := range defaultFacadeSearchHits {
		if hit.ID != id {
			continue
		}
		return map[string]any{
			"id":    hit.ID,
			"title": hit.Title,
			"text":  hit.Text,
			"url":   hit.URL,
			"metadata": map[string]any{
				"snippet": hit.Snippet,
			},
		}, true
	}
	return nil, false
}
