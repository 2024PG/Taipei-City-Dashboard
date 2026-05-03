package services

import (
	"context"
	"fmt"
	"testing"
)

func TestBuildComponentEvidencePackNoQdrantHits(t *testing.T) {
	restore := stubEvidenceDependencies(
		func(ctx context.Context, query string, limit int, scoreThreshold float32) ([]ComponentResult, error) {
			return []ComponentResult{}, nil
		},
		nil,
	)
	defer restore()

	pack, err := BuildComponentEvidencePack(context.Background(), ComponentEvidenceQuery{UserQuestion: "test question"})
	if err != nil {
		t.Fatalf("BuildComponentEvidencePack returned error: %v", err)
	}
	if pack.Answerability.Status != "not_answerable" {
		t.Fatalf("answerability status = %q, want not_answerable", pack.Answerability.Status)
	}
	if pack.Retrieval.CandidateCount != 0 {
		t.Fatalf("candidate count = %d, want 0", pack.Retrieval.CandidateCount)
	}
}

func TestBuildComponentEvidencePackUsesPreferredComponentWhenQdrantMisses(t *testing.T) {
	restore := stubEvidenceDependencies(
		func(ctx context.Context, query string, limit int, scoreThreshold float32) ([]ComponentResult, error) {
			return []ComponentResult{}, nil
		},
		func(index string, city string, timeFrom string, timeTo string) (ComponentChartDataResult, error) {
			return ComponentChartDataResult{
				Index:     index,
				City:      city,
				QueryType: "time",
				Unit:      "度",
				Data:      []map[string]interface{}{{"x": "2025-09-01T00:00:00+08:00", "y": 100}},
			}, nil
		},
	)
	defer restore()

	pack, err := BuildComponentEvidencePack(context.Background(), ComponentEvidenceQuery{
		UserQuestion: "2025年9月電力用多少度",
		City:         "taipei",
		PreferredComponent: &ComponentResult{
			ID:    42,
			Index: "power_usage",
			Name:  "用電(度)量",
			City:  "taipei",
		},
	})
	if err != nil {
		t.Fatalf("BuildComponentEvidencePack returned error: %v", err)
	}
	if pack.Answerability.Status != "answerable" {
		t.Fatalf("answerability status = %q, want answerable", pack.Answerability.Status)
	}
	if len(pack.Components) != 1 {
		t.Fatalf("components len = %d, want 1", len(pack.Components))
	}
	if pack.Components[0].Name != "用電(度)量" {
		t.Fatalf("component name = %q, want 用電(度)量", pack.Components[0].Name)
	}
	if pack.TimeRange.From != "2025-09-01T00:00:00+08:00" {
		t.Fatalf("time from = %q, want 2025-09-01T00:00:00+08:00", pack.TimeRange.From)
	}
}

func TestComponentContextToPreferredResultsMatchesElectricityDashboard(t *testing.T) {
	results := ComponentContextToPreferredResults("2025年9月電力用多少度", map[string]interface{}{
		"dashboard": map[string]interface{}{"name": "電力重生了"},
		"components": []interface{}{
			map[string]interface{}{"id": 1, "index": "electric_bus_ratio", "name": "電動巴士比例", "city": "taipei"},
			map[string]interface{}{"id": 2, "index": "power_usage", "name": "用電(度)量", "city": "taipei"},
		},
	})
	if len(results) == 0 {
		t.Fatal("expected preferred component results")
	}
	if results[0].Name != "用電(度)量" {
		t.Fatalf("top preferred component = %q, want 用電(度)量", results[0].Name)
	}
}

func TestBuildComponentEvidencePackDoesNotIncludeUnrelatedQdrantWhenPreferredIsStrong(t *testing.T) {
	restore := stubEvidenceDependencies(
		func(ctx context.Context, query string, limit int, scoreThreshold float32) ([]ComponentResult, error) {
			return []ComponentResult{
				{ID: 3, Index: "ebus_percent", Name: "電動巴士比例", City: "taipei", Score: 0.93},
				{ID: 4, Index: "bike_network", Name: "自行車道路統計資料", City: "taipei", Score: 0.90},
			}, nil
		},
		func(index string, city string, timeFrom string, timeTo string) (ComponentChartDataResult, error) {
			return ComponentChartDataResult{
				Index:     index,
				City:      city,
				QueryType: "time",
				Unit:      "度",
				Data:      []map[string]interface{}{{"x": "2025-09-01T00:00:00+08:00", "y": 100}},
			}, nil
		},
	)
	defer restore()

	pack, err := BuildComponentEvidencePack(context.Background(), ComponentEvidenceQuery{
		UserQuestion: "2025年9月電力用多少度",
		City:         "taipei",
		PreferredComponents: []ComponentResult{
			{ID: 2, Index: "power_usage", Name: "用電(度)量", City: "taipei", Score: 2.75},
			{ID: 3, Index: "ebus_percent", Name: "電動巴士比例", City: "taipei", Score: 1.3},
		},
	})
	if err != nil {
		t.Fatalf("BuildComponentEvidencePack returned error: %v", err)
	}
	if len(pack.Components) != 1 {
		t.Fatalf("components len = %d, want 1", len(pack.Components))
	}
	if pack.Components[0].Index != "power_usage" {
		t.Fatalf("component index = %q, want power_usage", pack.Components[0].Index)
	}
	if pack.Retrieval.CandidateCount != 1 {
		t.Fatalf("candidate count = %d, want 1", pack.Retrieval.CandidateCount)
	}
}

func TestBuildComponentEvidencePackPartialWhenOneComponentFails(t *testing.T) {
	restore := stubEvidenceDependencies(
		func(ctx context.Context, query string, limit int, scoreThreshold float32) ([]ComponentResult, error) {
			return []ComponentResult{
				{ID: 1, Index: "ok_component", Name: "OK Component", City: "taipei", Score: 0.91},
				{ID: 2, Index: "failed_component", Name: "Failed Component", City: "taipei", Score: 0.88},
			}, nil
		},
		func(index string, city string, timeFrom string, timeTo string) (ComponentChartDataResult, error) {
			if index == "failed_component" {
				return ComponentChartDataResult{Index: index, City: city, QueryType: "time"}, fmt.Errorf("database timeout")
			}
			return ComponentChartDataResult{
				Index:     index,
				City:      city,
				QueryType: "time",
				Unit:      "unit",
				Data:      []map[string]interface{}{{"x": "2026-05-01T00:00:00+08:00", "y": 1}},
			}, nil
		},
	)
	defer restore()

	pack, err := BuildComponentEvidencePack(context.Background(), ComponentEvidenceQuery{UserQuestion: "test question"})
	if err != nil {
		t.Fatalf("BuildComponentEvidencePack returned error: %v", err)
	}
	if pack.Answerability.Status != "partial" {
		t.Fatalf("answerability status = %q, want partial", pack.Answerability.Status)
	}
	if len(pack.Components) != 1 {
		t.Fatalf("components len = %d, want 1", len(pack.Components))
	}
	if len(pack.InsufficientComponents) != 1 {
		t.Fatalf("insufficient components len = %d, want 1", len(pack.InsufficientComponents))
	}
	if pack.InsufficientComponents[0].Status != "error" {
		t.Fatalf("failed component status = %q, want error", pack.InsufficientComponents[0].Status)
	}
}

func TestBuildComponentEvidencePackAnswerableWhenAllComponentsSucceed(t *testing.T) {
	restore := stubEvidenceDependencies(
		func(ctx context.Context, query string, limit int, scoreThreshold float32) ([]ComponentResult, error) {
			return []ComponentResult{
				{ID: 1, Index: "component_a", Name: "Component A", City: "taipei", Score: 0.91},
				{ID: 2, Index: "component_b", Name: "Component B", City: "taipei", Score: 0.89},
			}, nil
		},
		func(index string, city string, timeFrom string, timeTo string) (ComponentChartDataResult, error) {
			return ComponentChartDataResult{
				Index:     index,
				City:      city,
				QueryType: "two_d",
				Unit:      "unit",
				Data:      []map[string]interface{}{{"x": "label", "y": 1}},
			}, nil
		},
	)
	defer restore()

	pack, err := BuildComponentEvidencePack(context.Background(), ComponentEvidenceQuery{UserQuestion: "test question"})
	if err != nil {
		t.Fatalf("BuildComponentEvidencePack returned error: %v", err)
	}
	if pack.Answerability.Status != "answerable" {
		t.Fatalf("answerability status = %q, want answerable", pack.Answerability.Status)
	}
	if len(pack.Components) != 2 {
		t.Fatalf("components len = %d, want 2", len(pack.Components))
	}
}

func TestBuildComponentEvidencePackNormalizesLimitsAndCity(t *testing.T) {
	var observedLimit int
	restore := stubEvidenceDependencies(
		func(ctx context.Context, query string, limit int, scoreThreshold float32) ([]ComponentResult, error) {
			observedLimit = limit
			return []ComponentResult{}, nil
		},
		nil,
	)
	defer restore()

	pack, err := BuildComponentEvidencePack(context.Background(), ComponentEvidenceQuery{
		UserQuestion: "test question",
		City:         "invalid",
		TopK:         99,
	})
	if err != nil {
		t.Fatalf("BuildComponentEvidencePack returned error: %v", err)
	}
	if pack.City != "taipei" {
		t.Fatalf("city = %q, want taipei", pack.City)
	}
	if pack.Retrieval.TopK != maxEvidenceTopK {
		t.Fatalf("retrieval top_k = %d, want %d", pack.Retrieval.TopK, maxEvidenceTopK)
	}
	if observedLimit != maxEvidenceTopK {
		t.Fatalf("search limit = %d, want %d", observedLimit, maxEvidenceTopK)
	}
}

func TestBuildComponentEvidencePackKeepsMetrotaipei(t *testing.T) {
	restore := stubEvidenceDependencies(
		func(ctx context.Context, query string, limit int, scoreThreshold float32) ([]ComponentResult, error) {
			return []ComponentResult{}, nil
		},
		nil,
	)
	defer restore()

	pack, err := BuildComponentEvidencePack(context.Background(), ComponentEvidenceQuery{
		UserQuestion: "test question",
		City:         "metrotaipei",
	})
	if err != nil {
		t.Fatalf("BuildComponentEvidencePack returned error: %v", err)
	}
	if pack.City != "metrotaipei" {
		t.Fatalf("city = %q, want metrotaipei", pack.City)
	}
}

func TestComponentContextToRelevantActiveResultIgnoresUnrelatedActiveComponent(t *testing.T) {
	result := ComponentContextToRelevantActiveResult("哪一區老屋最多", map[string]interface{}{
		"dashboard": map[string]interface{}{"name": "電力重生了"},
		"active_component": map[string]interface{}{
			"id":    2,
			"index": "power_usage",
			"name":  "用電(度)量",
			"city":  "taipei",
		},
	})
	if result != nil {
		t.Fatalf("active component = %#v, want nil for unrelated question", result)
	}
}

func TestComponentContextToRelevantActiveResultKeepsExplicitCurrentComponent(t *testing.T) {
	result := ComponentContextToRelevantActiveResult("目前組件 2025年9月電力用多少度", map[string]interface{}{
		"dashboard": map[string]interface{}{"name": "電力重生了"},
		"active_component": map[string]interface{}{
			"id":    2,
			"index": "power_usage",
			"name":  "用電(度)量",
			"city":  "taipei",
		},
	})
	if result == nil {
		t.Fatal("expected active component for explicit current-component question")
	}
	if result.Index != "power_usage" {
		t.Fatalf("active index = %q, want power_usage", result.Index)
	}
	if result.Score <= 1 {
		t.Fatalf("active score = %.2f, want boosted relevance score", result.Score)
	}
}

func stubEvidenceDependencies(
	search func(context.Context, string, int, float32) ([]ComponentResult, error),
	fetch func(string, string, string, string) (ComponentChartDataResult, error),
) func() {
	oldSearch := searchQdrantComponentsForEvidence
	oldFetch := fetchComponentDataForEvidence
	if search != nil {
		searchQdrantComponentsForEvidence = search
	}
	if fetch != nil {
		fetchComponentDataForEvidence = fetch
	}
	return func() {
		searchQdrantComponentsForEvidence = oldSearch
		fetchComponentDataForEvidence = oldFetch
	}
}
