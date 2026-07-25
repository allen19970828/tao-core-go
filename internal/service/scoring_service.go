package service

import (
	"math/rand"
	"sort"
	"strings"
	"time"

	"tao-core-go/internal/domain/models"
)

// ScoringService 提供自動計分引擎與選項平衡演算法介面。
type ScoringService interface {
	ScoreItem(item *models.Item, responseData string) (score float64, isCorrect bool)
	BalanceOptionsKey(options []models.Option) []models.Option
}

type scoringService struct{}

// NewScoringService 建立並回傳 ScoringService 實體。
func NewScoringService() ScoringService {
	return &scoringService{}
}

// ScoreItem 比對考生輸入的答案與題目標準答案，計算得分。
// 支援題型：
// - SINGLE_CHOICE (單選題)：精準字串比對
// - MULTIPLE_CHOICE (多選題)：排序後比對逗號分隔字串 (例如 "A,C")
// - SHORT_ANSWER (簡答題)：去除前後空白與大小寫精準比對
func (s *scoringService) ScoreItem(item *models.Item, responseData string) (float64, bool) {
	cleanResp := strings.TrimSpace(responseData)
	cleanAnswer := strings.TrimSpace(item.CorrectAnswer)

	switch item.ItemType {
	case models.ItemTypeSingleChoice:
		if strings.EqualFold(cleanResp, cleanAnswer) {
			return item.MaxScore, true
		}
	case models.ItemTypeMultipleChoice:
		respParts := strings.Split(cleanResp, ",")
		ansParts := strings.Split(cleanAnswer, ",")

		for i := range respParts {
			respParts[i] = strings.TrimSpace(respParts[i])
		}
		for i := range ansParts {
			ansParts[i] = strings.TrimSpace(ansParts[i])
		}

		sort.Strings(respParts)
		sort.Strings(ansParts)

		if strings.Join(respParts, ",") == strings.Join(ansParts, ",") {
			return item.MaxScore, true
		}
	case models.ItemTypeShortAnswer:
		if strings.EqualFold(cleanResp, cleanAnswer) {
			return item.MaxScore, true
		}
	}

	return 0.0, false
}

// BalanceOptionsKey 實作「選項鍵 25% 均勻洗牌演算法」。
// 洗牌並重排選項的 Identifier (A, B, C, D)，確保各選項在整張試卷中的正確答案比例接近 25%。
func (s *scoringService) BalanceOptionsKey(options []models.Option) []models.Option {
	if len(options) == 0 {
		return options
	}

	r := rand.New(rand.NewSource(time.Now().UnixNano()))

	shuffled := make([]models.Option, len(options))
	copy(shuffled, options)

	r.Shuffle(len(shuffled), func(i, j int) {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	})

	identifiers := []string{"A", "B", "C", "D", "E", "F"}
	for i := range shuffled {
		if i < len(identifiers) {
			shuffled[i].Identifier = identifiers[i]
		}
	}

	return shuffled
}
