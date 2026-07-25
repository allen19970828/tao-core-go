package service

import (
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

func TestBalanceOptionsKey(t *testing.T) {
	svc := NewScoringService()

	opts := []models.Option{
		{Identifier: "A", Text: "選項一"},
		{Identifier: "B", Text: "選項二"},
		{Identifier: "C", Text: "選項三"},
		{Identifier: "D", Text: "選項四"},
	}

	balanced := svc.BalanceOptionsKey(opts)

	if len(balanced) != 4 {
		t.Fatalf("Expected 4 options, got %d", len(balanced))
	}
	if balanced[0].Identifier != "A" || balanced[1].Identifier != "B" {
		t.Errorf("Option identifiers should be reassigned A, B, C, D")
	}
}
