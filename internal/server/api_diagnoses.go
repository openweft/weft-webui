// api_diagnoses.go — admin-only Diagnoses panel, fed by the
// weft-doctor pipeline over NATS (subject weft.diagnosis.>).
//
//   GET /api/diagnoses              — current cache snapshot
//
// The SSE live stream lives in diagnoses_sse.go (hand-rolled, like
// /api/events). The huma endpoint here serves the initial render +
// fallback for browsers without EventSource. Both read from the same
// in-process diagnoses.Cache.
//
// Scope : ScopeAdmin only. A regular user has no business seeing
// cluster-wide diagnoses ; tenant-portal users would surface the
// noise without the context to act on it.

package server

import (
	"context"

	"github.com/danielgtaylor/huma/v2"

	"github.com/openweft/weft-webui/internal/diagnoses"
)

// diagnosisOutput is the wire shape. Mirror of diagnoses.Diagnosis
// with omitempty discipline already on the struct tags ; we just
// re-publish for the openapi.json so the TS client sees it.
type diagnosisOutput struct {
	PatternHash     string                `json:"pattern_hash"`
	Severity        string                `json:"severity"`
	Title           string                `json:"title"`
	RootCause       string                `json:"root_cause,omitempty"`
	SuggestedAction string                `json:"suggested_action,omitempty"`
	FileLocation    string                `json:"file_location,omitempty"`
	Occurrences     int                   `json:"occurrences"`
	FirstSeen       string                `json:"first_seen,omitempty"`
	LastSeen        string                `json:"last_seen,omitempty"`
	Examples        []logEventOutput      `json:"examples,omitempty"`
}

type logEventOutput struct {
	Time   string         `json:"time,omitempty"`
	Level  string         `json:"level,omitempty"`
	Msg    string         `json:"msg,omitempty"`
	Attrs  map[string]any `json:"attrs,omitempty"`
	Source string         `json:"source,omitempty"`
}

type listDiagnosesOutput struct {
	Body struct {
		Items []diagnosisOutput `json:"items"`
	}
}

// mountDiagnosesAPI registers GET /api/diagnoses on the Infra portal.
// Reads from the package-level diagCache (set by buildHandler from
// Deps.DiagnosesCache). nil cache → empty list, never 500.
func mountDiagnosesAPI(api huma.API, scope Scope) {
	if !scope.Has(ScopeAdmin) {
		return
	}
	huma.Register(api, huma.Operation{
		OperationID: "list-diagnoses",
		Method:      "GET",
		Path:        "/api/diagnoses",
		Summary:     "List active cluster diagnoses",
		Description: "Returns the current weft-doctor diagnoses cached " +
			"in-process, sorted by severity (critical first) then by " +
			"occurrence count. Cache is fed from the NATS subject " +
			"weft.diagnosis.> ; an offline cache (no NATS configured) " +
			"returns an empty list.",
		Tags: []string{"diagnoses"},
	}, func(_ context.Context, _ *struct{}) (*listDiagnosesOutput, error) {
		out := &listDiagnosesOutput{}
		if diagCache == nil {
			return out, nil
		}
		for _, d := range diagCache.Snapshot() {
			out.Body.Items = append(out.Body.Items, convert(d))
		}
		return out, nil
	})
}

// convert maps the internal Diagnosis to the wire output. Time
// fields become RFC-3339 strings so the TS client gets a portable
// representation that doesn't depend on JS Date parsing edge cases.
func convert(d diagnoses.Diagnosis) diagnosisOutput {
	out := diagnosisOutput{
		PatternHash:     d.PatternHash,
		Severity:        string(d.Severity),
		Title:           d.Title,
		RootCause:       d.RootCause,
		SuggestedAction: d.SuggestedAction,
		FileLocation:    d.FileLocation,
		Occurrences:     d.Occurrences,
	}
	if !d.FirstSeen.IsZero() {
		out.FirstSeen = d.FirstSeen.UTC().Format("2006-01-02T15:04:05Z")
	}
	if !d.LastSeen.IsZero() {
		out.LastSeen = d.LastSeen.UTC().Format("2006-01-02T15:04:05Z")
	}
	for _, ex := range d.Examples {
		eo := logEventOutput{
			Level:  ex.Level,
			Msg:    ex.Msg,
			Attrs:  ex.Attrs,
			Source: ex.Source,
		}
		if !ex.Time.IsZero() {
			eo.Time = ex.Time.UTC().Format("2006-01-02T15:04:05Z")
		}
		out.Examples = append(out.Examples, eo)
	}
	return out
}
