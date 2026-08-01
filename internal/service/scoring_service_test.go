package service

import (
	"math"
	"testing"

	"tao-core-go/internal/domain/models"
)

func TestScoreItem(t *testing.T) {
	svc := NewScoringService()

	item := &models.Item{
		ID:            "item-1",
		ItemType:      models.ItemTypeSingleChoice,
		CorrectAnswer: "A",
		MaxScore:      10.0,
	}

	score, isCorrect := svc.ScoreItem(item, "A")
	if !isCorrect || score != 10.0 {
		t.Errorf("Expected score 10.0 and correct, got score %f, correct %v", score, isCorrect)
	}

	scoreWrong, isCorrectWrong := svc.ScoreItem(item, "B")
	if isCorrectWrong || scoreWrong != 0.0 {
		t.Errorf("Expected score 0.0 and wrong, got score %f, correct %v", scoreWrong, isCorrectWrong)
	}
}

func TestScoringRejectsInvalidMaximumScore(t *testing.T) {
	service := NewScoringService()
	for _, maximum := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		score, correct := service.ScoreItem(&models.Item{ItemType: models.ItemTypeSingleChoice, CorrectAnswer: "A", MaxScore: maximum}, "A")
		if correct || score != 0 {
			t.Fatalf("expected invalid maximum %v to be rejected", maximum)
		}
	}
}

func TestScoringRejectsBlankAnswers(t *testing.T) {
	service := NewScoringService()
	for _, item := range []*models.Item{
		{ItemType: models.ItemTypeSingleChoice, CorrectAnswer: "", MaxScore: 1},
		{ItemType: models.ItemTypeSingleChoice, CorrectAnswer: "A", MaxScore: 1},
	} {
		if score, correct := service.ScoreItem(item, " "); correct || score != 0 {
			t.Fatal("expected blank answer or response to receive no score")
		}
	}
}

func TestBalanceOptionsKey(t *testing.T) {
	svc := NewScoringService()

	opts := []models.Option{
		{Identifier: "A", Text: "選項一"},
		{Identifier: "B", Text: "選項二"},
		{Identifier: "C", Text: "選項三"},
		{Identifier: "D", Text: "選項四"},
	}

	balanced, mapping, err := svc.BalanceOptionsKey(opts)
	if err != nil {
		t.Fatalf("BalanceOptionsKey failed: %v", err)
	}

	if len(balanced) != 4 {
		t.Fatalf("Expected 4 options, got %d", len(balanced))
	}
	if balanced[0].Identifier != "A" || balanced[1].Identifier != "B" {
		t.Errorf("Option identifiers should be reassigned A, B, C, D")
	}
	if len(mapping) != len(opts) {
		t.Fatalf("Expected a complete identifier mapping, got %v", mapping)
	}
}
