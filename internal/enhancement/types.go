package enhancement

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"

	"github.com/bitmagnet-io/bitmagnet/internal/model"
)

type File struct {
	Path string `json:"path"`
	Size uint   `json:"size"`
}
type Evidence struct {
	InfoHash        string          `json:"infoHash"`
	Name            string          `json:"name"`
	Size            uint            `json:"size"`
	FileCount       *uint           `json:"fileCount,omitempty"`
	IncompleteFiles bool            `json:"incompleteFiles"`
	Files           []File          `json:"files,omitempty"`
	Classification  json.RawMessage `json:"classification,omitempty"`
	Hint            json.RawMessage `json:"hint,omitempty"`
}

const (
	OutcomeLimited         = "limited"
	OutcomeMalformed       = "malformed"
	OutcomeProviderFailure = "provider_failure"
	WorkMovie              = "movie"
	WorkSeries             = "tv_show"
	canceledMessage        = "evaluation deadline or cancellation"
)

type Work struct {
	Type string `json:"type"`
	ID   int64  `json:"id"`
}
type Candidate struct {
	Work
	Title         string `json:"title"`
	OriginalTitle string `json:"originalTitle"`
	Date          string `json:"date"`
	Overview      string `json:"overview"`
}
type Metadata struct {
	Type            string        `json:"type,omitempty"`
	Title           string        `json:"title,omitempty"`
	Year            int           `json:"year,omitempty"`
	Languages       []string      `json:"languages,omitempty"`
	Episodes        map[int][]int `json:"episodes,omitempty"`
	VideoResolution string        `json:"videoResolution,omitempty"`
	VideoSource     string        `json:"videoSource,omitempty"`
	VideoCodec      string        `json:"videoCodec,omitempty"`
	Video3D         string        `json:"video3D,omitempty"`
	VideoModifier   string        `json:"videoModifier,omitempty"`
	ReleaseGroup    string        `json:"releaseGroup,omitempty"`
	Tags            []string      `json:"tags,omitempty"`
	MixedWorks      bool          `json:"mixedWorks,omitempty"`
	Work            *Work         `json:"work,omitempty"`
}
type Usage struct {
	Estimated       bool     `json:"estimated"`
	Requests        int      `json:"requests"`
	ToolCalls       int      `json:"toolCalls"`
	InputTokens     int      `json:"inputTokens"`
	OutputTokens    int      `json:"outputTokens"`
	ReasoningTokens int      `json:"reasoningTokens"`
	Cost            *float64 `json:"cost,omitempty"`
}
type Evaluation struct {
	InfoHash  string    `json:"infoHash"`
	Outcome   string    `json:"outcome"`
	Result    *Metadata `json:"result,omitempty"`
	Usage     Usage     `json:"usage"`
	LatencyMS int64     `json:"latencyMs"`
	Error     string    `json:"error,omitempty"`
}

var tagPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func decodeStrict(data []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()

	if err := d.Decode(v); err != nil {
		return err
	}

	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("expected exactly one JSON object")
	}

	return nil
}

func validateMetadata(data string, c Config, candidates map[Work]struct{}) (*Metadata, error) {
	var m Metadata
	if err := decodeStrict([]byte(data), &m); err != nil {
		return nil, err
	}

	if strings.TrimSpace(data) == "null" {
		return nil, errors.New("expected metadata object")
	}

	if m.Type != "" {
		if _, err := model.ParseContentType(m.Type); err != nil {
			return nil, err
		}
	}

	if m.Year != 0 && (m.Year < 1800 || m.Year > 2200) {
		return nil, errors.New("year out of range")
	}

	if len(m.Title) > 500 || len(m.ReleaseGroup) > 100 || len(m.Tags) > len(c.Tags) || len(m.Languages) > 100 ||
		len(m.Episodes) > 200 {
		return nil, errors.New("metadata too large")
	}

	for _, l := range m.Languages {
		if !model.Language(l).IsValid() {
			return nil, errors.New("invalid release language")
		}
	}

	checks := []struct {
		v     string
		parse func(string) error
	}{
		{m.VideoResolution, func(s string) error { _, err := model.ParseVideoResolution(s); return err }},
		{m.VideoSource, func(s string) error { _, err := model.ParseVideoSource(s); return err }},
		{m.VideoCodec, func(s string) error { _, err := model.ParseVideoCodec(s); return err }},
		{m.Video3D, func(s string) error { _, err := model.ParseVideo3D(s); return err }},
		{m.VideoModifier, func(s string) error { _, err := model.ParseVideoModifier(s); return err }},
	}
	for _, check := range checks {
		if check.v != "" {
			if err := check.parse(check.v); err != nil {
				return nil, err
			}
		}
	}

	for season, eps := range m.Episodes {
		if season < 0 || season > 1000 || len(eps) > 10000 {
			return nil, errors.New("invalid season")
		}

		for _, ep := range eps {
			if ep < 1 || ep > 10000 {
				return nil, errors.New("invalid episode")
			}
		}
	}

	allowed := make(map[string]bool, len(c.Tags))
	for _, tag := range c.Tags {
		allowed[tag] = true
	}

	seen := map[string]bool{}
	for _, tag := range m.Tags {
		if !allowed[tag] || seen[tag] {
			return nil, errors.New("invalid generated tag")
		}

		seen[tag] = true
	}

	if m.Work != nil {
		if m.MixedWorks || m.Work.ID <= 0 || (m.Work.Type != WorkMovie && m.Work.Type != WorkSeries) ||
			m.Type != m.Work.Type {
			return nil, errors.New("invalid work identity")
		}

		if _, ok := candidates[*m.Work]; !ok {
			return nil, errors.New("work identity was not retrieved")
		}
	}

	return &m, nil
}
