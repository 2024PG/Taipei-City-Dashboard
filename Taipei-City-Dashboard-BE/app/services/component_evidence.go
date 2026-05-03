package services

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"TaipeiCityDashboardBE/global"
	"TaipeiCityDashboardBE/logs"
)

const (
	defaultEvidenceTopK           = 5
	maxEvidenceTopK               = 8
	defaultEvidenceScoreThreshold = 0.75
	maxEvidenceArrayItems         = 300
)

var (
	searchQdrantComponentsForEvidence = SearchQdrantComponents
	fetchComponentDataForEvidence     = FetchComponentChartDataByIndexAndTime
)

type ComponentEvidenceQuery struct {
	UserQuestion        string            `json:"user_question"`
	City                string            `json:"city"`
	TimeFrom            string            `json:"time_from"`
	TimeTo              string            `json:"time_to"`
	TopK                int               `json:"top_k"`
	ScoreThreshold      float32           `json:"score_threshold"`
	PreferredComponent  *ComponentResult  `json:"preferred_component,omitempty"`
	PreferredComponents []ComponentResult `json:"preferred_components,omitempty"`
	// ForcedIndexes bypasses Qdrant score filtering and directly includes these component
	// indexes in the evidence. Useful when the AI can identify the correct index from the
	// component list but the semantic search score may be low for colloquial queries.
	ForcedIndexes []string `json:"forced_indexes,omitempty"`
}

type EvidenceTimeRange struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Defaulted bool   `json:"defaulted"`
}

type EvidenceRetrieval struct {
	CollectionName string  `json:"collection_name"`
	TopK           int     `json:"top_k"`
	ScoreThreshold float32 `json:"score_threshold"`
	CandidateCount int     `json:"candidate_count"`
	ReturnedCount  int     `json:"returned_count"`
}

type EvidenceAnswerability struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

type ComponentEvidence struct {
	ID        interface{} `json:"id,omitempty"`
	Index     string      `json:"index"`
	Name      string      `json:"name"`
	City      string      `json:"city"`
	Score     float32     `json:"score"`
	QueryType string      `json:"query_type,omitempty"`
	Unit      string      `json:"unit"`
	Status    string      `json:"status"`
	Reason    string      `json:"reason,omitempty"`
	Data      interface{} `json:"data,omitempty"`
	Truncated bool        `json:"truncated,omitempty"`
}

type ComponentEvidencePack struct {
	Question               string                `json:"question"`
	City                   string                `json:"city"`
	TimeRange              EvidenceTimeRange     `json:"time_range"`
	Retrieval              EvidenceRetrieval     `json:"retrieval"`
	Answerability          EvidenceAnswerability `json:"answerability"`
	Components             []ComponentEvidence   `json:"components"`
	InsufficientComponents []ComponentEvidence   `json:"insufficient_components"`
	InstructionsForLLM     []string              `json:"instructions_for_llm"`
}

// BuildComponentEvidencePack finds relevant components, fetches each component's
// existing chart data, and returns structured evidence for an LLM answer.
func BuildComponentEvidencePack(ctx context.Context, query ComponentEvidenceQuery) (ComponentEvidencePack, error) {
	query = normalizeEvidenceQuery(query)

	pack := ComponentEvidencePack{
		Question: query.UserQuestion,
		City:     query.City,
		TimeRange: EvidenceTimeRange{
			From: query.TimeFrom,
			To:   query.TimeTo,
		},
		Retrieval: EvidenceRetrieval{
			CollectionName: qdrantCollectionName(),
			TopK:           query.TopK,
			ScoreThreshold: query.ScoreThreshold,
		},
		Components:             []ComponentEvidence{},
		InsufficientComponents: []ComponentEvidence{},
		InstructionsForLLM: []string{
			"Use only values present in components[].data.",
			"Do not invent, estimate, or infer missing numeric values.",
			"Do not generate SQL or ask for table and column names.",
			"CRITICAL: If components[].truncated is true, the data array is incomplete. You MUST NOT compute totals, city-wide sums, rankings, or averages from truncated data. Instead, explicitly state that the data is truncated and the result cannot be reliably computed.",
			"Every numeric value must include the component unit from components[].unit; if unit is empty or missing, write 單位未提供.",
			"Answer with 2-3 summary sentences, key indicators with units, comparative analysis, decision-support suggestions, and data limitations.",
			"Suggestions must be framed as decision support, not final policy conclusions.",
			"For policy effectiveness questions, if evidence only contains demographic structure, background indicators, or static values, state that the evidence is insufficient to judge policy effectiveness and can only describe demand background or pressure.",
			"For policy effectiveness questions, list missing evidence such as service usage rate, number of care sites, care workforce, waiting time, beds, satisfaction, time series, or before-after policy comparison.",
			"Use only components directly relevant to the user question; do not include unrelated retrieved components in the main analysis.",
			"If unrelated components appear in retrieval results, briefly say they were not used because their relation to the question is low.",
			"Recommendations must be supported by evidence. If evidence is insufficient, recommend only what additional data or indicators should be reviewed.",
			"Do not add percent signs to indicators with empty or missing unit, including aging index. Use 單位未提供 unless the component unit explicitly says otherwise.",
			"State which components were used.",
			"When answerability.status is partial or not_answerable, explicitly mention what data is unavailable.",
			"When not_answerable, include debug-friendly details from this evidence: active/preferred component id/index/name if present, retrieval.collection_name, top_k, candidate_count, returned_count, and whether the requested time range returned no data.",
		},
	}

	timeFrom, timeTo, defaulted := normalizeEvidenceTimeRange(query.UserQuestion, query.TimeFrom, query.TimeTo)
	pack.TimeRange = EvidenceTimeRange{From: timeFrom, To: timeTo, Defaulted: defaulted}

	// Convert ForcedIndexes to high-priority candidates that bypass Qdrant score filtering.
	forcedCandidates := make([]ComponentResult, 0, len(query.ForcedIndexes))
	for _, idx := range query.ForcedIndexes {
		if idx == "" {
			continue
		}
		city := query.City
		if city == "" {
			city = "taipei"
		}
		forcedCandidates = append(forcedCandidates, ComponentResult{
			Index: idx,
			City:  city,
			Score: 3.0, // higher than any Qdrant or preferred score → always included first
		})
		logs.FInfo("RAG forced index: %s city=%s", idx, city)
	}

	preferredCandidates := normalizePreferredComponents(query.PreferredComponent, query.PreferredComponents)
	preferredCandidates = narrowHighConfidencePreferredComponents(preferredCandidates)
	candidates, err := searchQdrantComponentsForEvidence(ctx, query.UserQuestion, query.TopK, query.ScoreThreshold)
	if err != nil {
		if len(preferredCandidates) > 0 {
			logs.FWarn("semantic search failed, falling back to preferred dashboard components: %v", err)
			candidates = []ComponentResult{}
		} else {
			pack.Answerability = EvidenceAnswerability{
				Status: "not_answerable",
				Reason: fmt.Sprintf("semantic search failed: %v", err),
			}
			return pack, nil
		}
	}
	// Forced indexes take the highest priority: prepend before preferred, then Qdrant candidates.
	if len(forcedCandidates) > 0 {
		candidates = mergePreferredComponents(forcedCandidates, candidates, query.TopK)
	}
	candidates = mergePreferredComponents(preferredCandidates, candidates, query.TopK)
	// Keyword-based injection: supplement Qdrant when domain vocabulary doesn't match embeddings.
	candidates = injectKeywordComponents(query.UserQuestion, candidates, query.City)
	if len(candidates) == 0 && err != nil {
		pack.Answerability = EvidenceAnswerability{
			Status: "not_answerable",
			Reason: fmt.Sprintf("semantic search failed: %v", err),
		}
		return pack, nil
	}
	if query.PreferredComponent != nil && query.PreferredComponent.Index != "" {
		logs.FInfo("RAG preferred active component: index=%s name=%s city=%s", query.PreferredComponent.Index, query.PreferredComponent.Name, query.PreferredComponent.City)
	}
	if len(preferredCandidates) > 0 {
		logs.FInfo("RAG preferred visible components: %s", preferredComponentsLog(preferredCandidates))
	}
	if len(preferredCandidates) == 1 && preferredCandidates[0].Score >= 1.5 {
		candidates = preferredCandidates
	}
	pack.Retrieval.CandidateCount = len(candidates)

	for _, candidate := range candidates {
		fetchCity := query.City
		if candidate.City != "" {
			fetchCity = candidate.City
		}
		component := ComponentEvidence{
			ID:     candidate.ID,
			Index:  candidate.Index,
			Name:   candidate.Name,
			City:   fetchCity,
			Score:  candidate.Score,
			Status: "ok",
		}

		result, fetchErr := fetchComponentDataForEvidence(candidate.Index, fetchCity, timeFrom, timeTo)
		component.QueryType = result.QueryType
		component.Unit = result.Unit
		if result.City != "" {
			component.City = result.City
		}

		// For CascadeTimelineChart components, annotate Taiwan quarter codes and filter by
		// the year/quarter the user asked about (all happens in-memory, no DB change needed).
		if result.QueryType == "CascadeTimelineChart" && result.Data != nil {
			quarterCodes := extractTaiwanQuarterCodesFromQuestion(query.UserQuestion)
			result.Data = annotateCascadeTimelineData(result.Data, quarterCodes)
		}

		switch {
		case fetchErr != nil && isUnsupportedComponentError(fetchErr):
			component.Status = "unsupported"
			component.Reason = fetchErr.Error()
			pack.InsufficientComponents = append(pack.InsufficientComponents, component)
		case fetchErr != nil:
			component.Status = "error"
			component.Reason = fetchErr.Error()
			pack.InsufficientComponents = append(pack.InsufficientComponents, component)
		case isEmptyComponentData(result.Data):
			component.Status = "no_data"
			component.Reason = "No chart data returned for this component and time range."
			pack.InsufficientComponents = append(pack.InsufficientComponents, component)
		default:
			component.Data, component.Truncated = truncateForEvidence(result.Data, maxEvidenceArrayItems)
			pack.Components = append(pack.Components, component)
		}
	}

	pack.Retrieval.ReturnedCount = len(pack.Components)
	pack.Answerability = determineAnswerability(len(pack.Components), len(pack.InsufficientComponents), len(candidates))
	return pack, nil
}

func normalizeEvidenceQuery(query ComponentEvidenceQuery) ComponentEvidenceQuery {
	if query.City != "metrotaipei" {
		query.City = "taipei"
	}
	if query.TopK <= 0 {
		query.TopK = defaultEvidenceTopK
	}
	if query.TopK > maxEvidenceTopK {
		query.TopK = maxEvidenceTopK
	}
	if query.ScoreThreshold <= 0 || query.ScoreThreshold > 1 {
		query.ScoreThreshold = defaultEvidenceScoreThreshold
	}
	return query
}

func qdrantCollectionName() string {
	if name := os.Getenv("QDRANT_COLLECTION_NAME"); name != "" {
		return name
	}
	if global.Qdrant.Collection != "" {
		return global.Qdrant.Collection
	}
	return "query_charts"
}

// normalizeEvidenceTimeRange returns the time range to use for evidence retrieval.
// Unlike the dashboard view (which defaults to 24h), evidence queries default to a
// 5-year window so that quarterly/annual static components (e.g. house_age) return
// data without needing a retry. Real-time components are unaffected: their DB queries
// already return the most recent records within any window that includes today.
func normalizeEvidenceTimeRange(question string, timeFrom string, timeTo string) (string, string, bool) {
	if timeFrom == "" && timeTo == "" {
		if from, to, ok := extractYearMonthRange(question); ok {
			return from, to, false
		}
		loc, _ := time.LoadLocation("Asia/Taipei")
		now := time.Now().In(loc)
		return now.AddDate(-5, 0, 0).Format(taipeiTimeLayout), now.Format(taipeiTimeLayout), true
	}
	return NormalizeComponentTimeRange(timeFrom, timeTo)
}

func extractYearMonthRange(question string) (string, string, bool) {
	matches := regexp.MustCompile(`(\d{4})\s*年\s*(\d{1,2})\s*月`).FindStringSubmatch(question)
	if len(matches) != 3 {
		return "", "", false
	}
	year, err := strconv.Atoi(matches[1])
	if err != nil {
		return "", "", false
	}
	month, err := strconv.Atoi(matches[2])
	if err != nil || month < 1 || month > 12 {
		return "", "", false
	}
	loc, _ := time.LoadLocation("Asia/Taipei")
	start := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, loc)
	end := start.AddDate(0, 1, 0).Add(-time.Second)
	return start.Format(taipeiTimeLayout), end.Format(taipeiTimeLayout), true
}

func normalizePreferredComponents(active *ComponentResult, preferred []ComponentResult) []ComponentResult {
	seen := map[string]struct{}{}
	results := make([]ComponentResult, 0, len(preferred)+1)
	if active != nil && active.Index != "" {
		active.Score = 1
		results = append(results, *active)
		seen[active.Index] = struct{}{}
	}
	for _, item := range preferred {
		if item.Index == "" {
			continue
		}
		if _, ok := seen[item.Index]; ok {
			continue
		}
		if item.Score <= 0 {
			item.Score = 0.99
		}
		results = append(results, item)
		seen[item.Index] = struct{}{}
	}
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	return results
}

func narrowHighConfidencePreferredComponents(preferred []ComponentResult) []ComponentResult {
	if len(preferred) == 0 {
		return preferred
	}
	if preferred[0].Score < 1.5 {
		return preferred
	}
	if len(preferred) == 1 || preferred[0].Score-preferred[1].Score >= 0.5 {
		return preferred[:1]
	}
	return preferred
}

func mergePreferredComponents(preferred []ComponentResult, candidates []ComponentResult, limit int) []ComponentResult {
	if len(preferred) == 0 {
		return candidates
	}
	merged := make([]ComponentResult, 0, len(preferred)+len(candidates))
	seen := map[string]struct{}{}
	for _, item := range preferred {
		if item.Index == "" {
			continue
		}
		merged = append(merged, item)
		seen[item.Index] = struct{}{}
		if len(merged) >= limit {
			return merged
		}
	}
	for _, candidate := range candidates {
		if candidate.Index == "" {
			continue
		}
		if _, ok := seen[candidate.Index]; ok {
			continue
		}
		merged = append(merged, candidate)
		seen[candidate.Index] = struct{}{}
		if len(merged) >= limit {
			break
		}
	}
	return merged
}

func ComponentContextToPreferredResult(componentContext map[string]interface{}) *ComponentResult {
	active, _ := componentContext["active_component"].(map[string]interface{})
	if len(active) == 0 {
		return nil
	}
	index, _ := active["index"].(string)
	if index == "" {
		return nil
	}
	name, _ := active["name"].(string)
	city, _ := active["city"].(string)
	return &ComponentResult{
		ID:    active["id"],
		Index: index,
		Name:  name,
		City:  city,
		Score: 1,
	}
}

func ComponentContextToRelevantActiveResult(question string, componentContext map[string]interface{}) *ComponentResult {
	active := ComponentContextToPreferredResult(componentContext)
	if active == nil {
		return nil
	}
	dashboard, _ := componentContext["dashboard"].(map[string]interface{})
	dashboardName, _ := dashboard["name"].(string)
	score := contextComponentMatchScore(question, dashboardName, active.Name)
	if questionReferencesActiveComponent(question) {
		score += 1.5
	}
	if score <= 0 {
		return nil
	}
	active.Score = score
	return active
}

func ComponentContextToPreferredResults(question string, componentContext map[string]interface{}) []ComponentResult {
	components, _ := componentContext["components"].([]interface{})
	if len(components) == 0 {
		return nil
	}
	dashboard, _ := componentContext["dashboard"].(map[string]interface{})
	dashboardName, _ := dashboard["name"].(string)
	results := make([]ComponentResult, 0)
	for _, raw := range components {
		component, _ := raw.(map[string]interface{})
		index, _ := component["index"].(string)
		name, _ := component["name"].(string)
		if index == "" || name == "" {
			continue
		}
		score := contextComponentMatchScore(question, dashboardName, name)
		if score <= 0 {
			continue
		}
		city, _ := component["city"].(string)
		results = append(results, ComponentResult{
			ID:    component["id"],
			Index: index,
			Name:  name,
			City:  city,
			Score: score,
		})
	}
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	return results
}

func contextComponentMatchScore(question string, dashboardName string, componentName string) float32 {
	q := strings.ToLower(question)
	d := strings.ToLower(dashboardName)
	name := strings.ToLower(componentName)
	var score float32
	if strings.Contains(q, name) {
		score += 1.2
	}
	for _, token := range componentNameTokens(name) {
		if token != "" && strings.Contains(q, token) {
			score += 0.35
		}
	}
	if strings.Contains(q, "電力") && strings.Contains(name, "用電") {
		score += 1.1
	}
	if strings.Contains(q, "電力") && strings.Contains(d, "電力") && strings.Contains(name, "電") {
		score += 0.8
	}
	if strings.Contains(q, "電") && strings.Contains(name, "電") {
		score += 0.5
	}
	if strings.Contains(q, "資料") && strings.Contains(d, "電力") && strings.Contains(name, "用電") {
		score += 0.4
	}
	return score
}

func questionReferencesActiveComponent(question string) bool {
	q := strings.ToLower(question)
	phrases := []string{
		"目前頁面", "目前組件", "這個組件", "此組件", "這張圖", "這個圖", "目前圖表", "當前組件",
		"active component", "current component", "this chart", "this component",
	}
	for _, phrase := range phrases {
		if strings.Contains(q, phrase) {
			return true
		}
	}
	return false
}

func componentNameTokens(name string) []string {
	cleaned := strings.NewReplacer("(", " ", ")", " ", "（", " ", "）", " ", "/", " ", "-", " ", "_", " ").Replace(name)
	return strings.Fields(cleaned)
}

// keywordComponentHints maps domain vocabulary to component indexes.
// When Qdrant semantic search misses a component due to vocabulary mismatch
// (e.g. colloquial "屋子" vs formal "舊屋"), this table injects the right index.
var keywordComponentHints = []struct {
	keywords []string
	index    string
}{
	{
		keywords: []string{
			"舊屋", "老屋", "屋齡", "老房子", "舊房子", "最舊", "老建物", "舊建物", "屋子", "建物年",
			"房子", "建物", "年以上", "五十年", "50年", "四十年", "40年", "三十年", "30年",
			"二十年", "20年", "老化", "屋況", "老齡", "老舊", "級距",
		},
		index: "house_age",
	},
}

// injectKeywordComponents supplements Qdrant results with indexes matched by keyword.
// It only injects when the index is not already present in candidates.
func injectKeywordComponents(question string, candidates []ComponentResult, city string) []ComponentResult {
	q := strings.ToLower(question)
	for _, hint := range keywordComponentHints {
		for _, kw := range hint.keywords {
			if strings.Contains(q, kw) {
				alreadyPresent := false
				for _, c := range candidates {
					if c.Index == hint.index {
						alreadyPresent = true
						break
					}
				}
				if !alreadyPresent {
					logs.FInfo("RAG keyword injection: %q matched %q → adding %s", question, kw, hint.index)
					candidates = append(candidates, ComponentResult{
						Index: hint.index,
						City:  city,
						Score: 2.5,
					})
				}
				break
			}
		}
	}
	return candidates
}

func preferredComponentsLog(components []ComponentResult) string {
	parts := make([]string, 0, len(components))
	for _, component := range components {
		parts = append(parts, fmt.Sprintf("%s/%s/%.2f", component.Index, component.Name, component.Score))
	}
	return strings.Join(parts, ", ")
}

func determineAnswerability(okCount int, insufficientCount int, candidateCount int) EvidenceAnswerability {
	switch {
	case candidateCount == 0:
		return EvidenceAnswerability{Status: "not_answerable", Reason: "No relevant components were found."}
	case okCount == 0:
		return EvidenceAnswerability{Status: "not_answerable", Reason: "Relevant components were found, but none returned usable data."}
	case insufficientCount > 0:
		return EvidenceAnswerability{Status: "partial", Reason: "Some relevant components returned data, while others were unavailable or unsupported."}
	default:
		return EvidenceAnswerability{Status: "answerable", Reason: "Relevant components returned usable data."}
	}
}

func isUnsupportedComponentError(err error) bool {
	return err != nil && strings.HasPrefix(err.Error(), "unsupported query type")
}

func isEmptyComponentData(data interface{}) bool {
	if data == nil {
		return true
	}

	raw, err := json.Marshal(data)
	if err != nil {
		return false
	}
	var normalized interface{}
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return false
	}
	return isEmptyJSONValue(normalized)
}

func isEmptyJSONValue(value interface{}) bool {
	switch v := value.(type) {
	case nil:
		return true
	case []interface{}:
		if len(v) == 0 {
			return true
		}
		for _, item := range v {
			if !isEmptyJSONValue(item) {
				return false
			}
		}
		return true
	case map[string]interface{}:
		if len(v) == 0 {
			return true
		}
		for _, item := range v {
			if !isEmptyJSONValue(item) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// taiwanQuarterToLabel converts a Taiwan quarter code like "1131" to a human-readable label.
// Returns the original string unchanged if it does not match the YYYS pattern.
func taiwanQuarterToLabel(code string) string {
	if len(code) != 4 {
		return code
	}
	rocYear, err1 := strconv.Atoi(code[:3])
	quarter, err2 := strconv.Atoi(code[3:])
	if err1 != nil || err2 != nil || quarter < 1 || quarter > 4 {
		return code
	}
	gregorianYear := rocYear + 1911
	return fmt.Sprintf("%s (民國%d年第%d季 / %d Q%d)", code, rocYear, quarter, gregorianYear, quarter)
}

// extractTaiwanQuarterCodesFromQuestion parses the user question for Taiwan year/quarter references.
// Returns nil when no specific year or quarter is mentioned (meaning: no filtering needed).
func extractTaiwanQuarterCodesFromQuestion(question string) []string {
	// Direct 4-digit Taiwan quarter code already in text (e.g. "1131")
	re4digit := regexp.MustCompile(`\b(1[0-9]{3})\b`)
	if matches := re4digit.FindAllString(question, -1); len(matches) > 0 {
		return matches
	}

	var rocYear int
	found := false

	// 民國 year: "民國113年"
	if m := regexp.MustCompile(`民國\s*(\d{2,3})\s*年`).FindStringSubmatch(question); len(m) > 1 {
		if y, err := strconv.Atoi(m[1]); err == nil {
			rocYear, found = y, true
		}
	}
	// Bare 3-digit ROC year: "113年"
	if !found {
		if m := regexp.MustCompile(`\b(\d{3})\s*年`).FindStringSubmatch(question); len(m) > 1 {
			if y, err := strconv.Atoi(m[1]); err == nil && y >= 100 && y <= 150 {
				rocYear, found = y, true
			}
		}
	}
	// Gregorian year: "2024年"
	if !found {
		if m := regexp.MustCompile(`(20\d{2})\s*年`).FindStringSubmatch(question); len(m) > 1 {
			if y, err := strconv.Atoi(m[1]); err == nil {
				rocYear, found = y-1911, true
			}
		}
	}
	if !found || rocYear < 100 || rocYear > 150 {
		return nil
	}

	// Check for a specific quarter within that year
	if m := regexp.MustCompile(`第\s*([1-4])\s*季|[Qq]\s*([1-4])`).FindStringSubmatch(question); len(m) >= 3 {
		q := m[1]
		if q == "" {
			q = m[2]
		}
		if q != "" {
			return []string{fmt.Sprintf("%d%s", rocYear, q)}
		}
	}

	// No specific quarter → return all four quarters for that year
	codes := make([]string, 4)
	for q := 1; q <= 4; q++ {
		codes[q-1] = fmt.Sprintf("%d%d", rocYear, q)
	}
	return codes
}

// annotateCascadeTimelineData converts Taiwan quarter codes in the time field to human-readable
// labels, and optionally filters rows to only those matching the requested quarter codes.
// Returns the original data unchanged if it cannot be processed as a flat row array.
func annotateCascadeTimelineData(data interface{}, quarterFilter []string) interface{} {
	raw, err := json.Marshal(data)
	if err != nil {
		return data
	}
	var rows []map[string]interface{}
	if err := json.Unmarshal(raw, &rows); err != nil || len(rows) == 0 {
		return data
	}

	filterSet := make(map[string]struct{}, len(quarterFilter))
	for _, code := range quarterFilter {
		filterSet[code] = struct{}{}
	}

	result := make([]map[string]interface{}, 0, len(rows))
	for _, row := range rows {
		// Extract raw time value (may be stored as string or number)
		timeVal := ""
		if t, ok := row["time"]; ok {
			switch v := t.(type) {
			case string:
				timeVal = v
			case float64:
				timeVal = fmt.Sprintf("%d", int(v))
			case int64:
				timeVal = fmt.Sprintf("%d", v)
			}
		}

		// Apply quarter filter
		if len(filterSet) > 0 {
			if _, ok := filterSet[timeVal]; !ok {
				continue
			}
		}

		// Annotate time field
		if label := taiwanQuarterToLabel(timeVal); label != timeVal {
			row["time"] = label
		}
		result = append(result, row)
	}

	// If the filter produced no results (e.g. data predates the requested quarter), keep all
	if len(filterSet) > 0 && len(result) == 0 {
		return data
	}
	return result
}

func truncateForEvidence(data interface{}, maxItems int) (interface{}, bool) {
	raw, err := json.Marshal(data)
	if err != nil {
		return data, false
	}

	var normalized interface{}
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return data, false
	}

	truncated := false
	return truncateJSONValue(normalized, maxItems, &truncated), truncated
}

func truncateJSONValue(value interface{}, maxItems int, truncated *bool) interface{} {
	switch v := value.(type) {
	case []interface{}:
		if len(v) > maxItems {
			v = v[:maxItems]
			*truncated = true
		}
		for i := range v {
			v[i] = truncateJSONValue(v[i], maxItems, truncated)
		}
		return v
	case map[string]interface{}:
		for key, item := range v {
			v[key] = truncateJSONValue(item, maxItems, truncated)
		}
		return v
	default:
		return value
	}
}
