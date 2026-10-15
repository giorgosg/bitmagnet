package enhancement

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"sort"
)

// Recipe includes answer-affecting settings only. Operational bounds, prices and
// credentials are deliberately absent. Revision covers tools/schema/matching code.
func Recipe(c Config, workflow string) (recipeFingerprint, canonical string, err error) {
	endpoint, err := url.Parse(c.BaseURL)
	if err != nil {
		return "", "", err
	}

	endpoint.User = nil
	endpoint.RawQuery = ""
	endpoint.Fragment = ""

	tags := append([]string(nil), c.Tags...)
	sort.Strings(tags)

	data, err := json.Marshal(struct {
		Revision            string   `json:"revision"`
		Workflow            string   `json:"workflow"`
		Endpoint            string   `json:"endpoint"`
		Model               string   `json:"model"`
		Temperature         float64  `json:"temperature"`
		Thinking            string   `json:"thinking"`
		Prompt              string   `json:"prompt"`
		Tags                []string `json:"tags"`
		OutputParameter     string   `json:"outputParameter"`
		RequestOutputTokens int      `json:"requestOutputTokens"`
	}{
		Revision:            "enhancement-v2-search-schema1-authority1",
		Workflow:            workflow,
		Endpoint:            endpoint.String(),
		Model:               c.Model,
		Temperature:         c.Temperature,
		Thinking:            c.Thinking,
		Prompt:              c.Prompt,
		Tags:                tags,
		OutputParameter:     c.OutputTokenParameter,
		RequestOutputTokens: c.RequestOutputTokens,
	})
	if err != nil {
		return "", "", err
	}

	return fingerprint(data), string(data), nil
}

func fingerprint(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
