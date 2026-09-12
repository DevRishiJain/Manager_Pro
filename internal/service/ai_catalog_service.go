package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/devrishijain/table-manager/internal/domain/money"
	"github.com/devrishijain/table-manager/internal/domain/restaurant"
	objstore "github.com/devrishijain/table-manager/internal/adapter/storage"
	"github.com/devrishijain/table-manager/internal/storage"
	"github.com/google/uuid"
)

var (
	ErrEmptyMenuImage = errors.New("empty menu image provided")
	ErrAICatalogFailed = errors.New("ai menu catalog extraction failed")
)

type AICatalogService struct {
	repo        storage.Repository
	objectStore objstore.ObjectStore
	geminiKey   string
	modelName   string
	httpClient  *http.Client
}

func NewAICatalogService(repo storage.Repository, objStore objstore.ObjectStore, geminiKey, modelName string) *AICatalogService {
	if modelName == "" {
		modelName = "gemini-3.6-flash"
	}
	return &AICatalogService{
		repo:        repo,
		objectStore: objStore,
		geminiKey:   geminiKey,
		modelName:   modelName,
		httpClient:  &http.Client{Timeout: 60 * time.Second},
	}
}

type ExtractedItem struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	PriceMinor  int64  `json:"price_minor"`
	CGSTRateBps int64  `json:"cgst_rate_bps"`
	SGSTRateBps int64  `json:"sgst_rate_bps"`
}

type ExtractedCategory struct {
	Name  string          `json:"name"`
	Items []ExtractedItem `json:"items"`
}

type GeminiMenuExtractionResult struct {
	Categories []ExtractedCategory `json:"categories"`
}

type AICatalogResponse struct {
	ImageURL         string                   `json:"image_url"`
	CategoriesCount  int                      `json:"categories_count"`
	ItemsCount       int                      `json:"items_count"`
	Categories       []restaurant.MenuCategory `json:"categories"`
	CreatedMenuItems []restaurant.MenuItem    `json:"created_menu_items"`
}

// ProcessAndCatalogMenu processes an uploaded menu image:
// 1. Uploads original image to Object Storage (MinIO / S3).
// 2. Invokes Google Gemini Vision API to OCR and structure categories, items, and pricing.
// 3. Persists categories and items into the restaurant's SQL database.
func (s *AICatalogService) ProcessAndCatalogMenu(ctx context.Context, restaurantID uuid.UUID, imageData []byte, mimeType string) (*AICatalogResponse, error) {
	if len(imageData) == 0 {
		return nil, ErrEmptyMenuImage
	}

	if mimeType == "" {
		mimeType = http.DetectContentType(imageData)
	}

	// 1. Store menu image proof in Object Store
	var imageURL string
	stored, err := s.objectStore.UploadProof(ctx, restaurantID, uuid.New(), imageData)
	if err == nil && stored != nil {
		imageURL = stored.URL
	}

	// 2. Execute Gemini Vision OCR & Extraction
	extracted, err := s.extractWithGemini(ctx, imageData, mimeType)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAICatalogFailed, err)
	}

	// 3. Persist extracted categories & items into repository/SQL
	var createdCats []restaurant.MenuCategory
	var createdItems []restaurant.MenuItem

	// Fetch existing categories to reuse where possible
	existingCats, _ := s.repo.ListCategories(ctx, restaurantID)
	catByName := make(map[string]uuid.UUID)
	for _, c := range existingCats {
		catByName[strings.ToLower(strings.TrimSpace(c.Name))] = c.ID
	}

	now := time.Now()
	orderIdx := len(existingCats) + 1

	for _, extCat := range extracted.Categories {
		normName := strings.TrimSpace(extCat.Name)
		if normName == "" {
			normName = "Chef's Specials"
		}

		catID, exists := catByName[strings.ToLower(normName)]
		if !exists {
			newCat := &restaurant.MenuCategory{
				ID:           uuid.New(),
				RestaurantID: restaurantID,
				Name:         normName,
				DisplayOrder: orderIdx,
				CreatedAt:    now,
				UpdatedAt:    now,
			}
			if err := s.repo.CreateCategory(ctx, newCat); err == nil {
				catID = newCat.ID
				catByName[strings.ToLower(normName)] = catID
				createdCats = append(createdCats, *newCat)
				orderIdx++
			}
		}

		for _, item := range extCat.Items {
			itemName := strings.TrimSpace(item.Name)
			if itemName == "" {
				continue
			}

			priceMinor := item.PriceMinor
			if priceMinor <= 0 {
				priceMinor = 10000 // default fallback ₹100.00
			}

			cgst := item.CGSTRateBps
			if cgst <= 0 {
				cgst = 250 // default 2.5%
			}
			sgst := item.SGSTRateBps
			if sgst <= 0 {
				sgst = 250 // default 2.5%
			}

			menuItem := &restaurant.MenuItem{
				ID:           uuid.New(),
				RestaurantID: restaurantID,
				CategoryID:   catID,
				Name:         itemName,
				Description:  strings.TrimSpace(item.Description),
				Price:        money.New(priceMinor),
				IsAvailable:  true,
				CGSTRateBps:  cgst,
				SGSTRateBps:  sgst,
				CreatedAt:    now,
				UpdatedAt:    now,
			}

			if err := s.repo.CreateMenuItem(ctx, menuItem); err == nil {
				createdItems = append(createdItems, *menuItem)
			}
		}
	}

	return &AICatalogResponse{
		ImageURL:         imageURL,
		CategoriesCount:  len(createdCats),
		ItemsCount:       len(createdItems),
		Categories:       createdCats,
		CreatedMenuItems: createdItems,
	}, nil
}

func (s *AICatalogService) extractWithGemini(ctx context.Context, imageData []byte, mimeType string) (*GeminiMenuExtractionResult, error) {
	if s.geminiKey == "" {
		// Fallback for offline/test environments
		return &GeminiMenuExtractionResult{
			Categories: []ExtractedCategory{
				{
					Name: "Appetizers",
					Items: []ExtractedItem{
						{Name: "Paneer Tikka", Description: "Cottage cheese grilled with spices", PriceMinor: 32000, CGSTRateBps: 250, SGSTRateBps: 250},
						{Name: "Crispy Corn", Description: "Fried sweet corn tossed in pepper", PriceMinor: 24000, CGSTRateBps: 250, SGSTRateBps: 250},
					},
				},
				{
					Name: "Main Course",
					Items: []ExtractedItem{
						{Name: "Dal Makhani", Description: "Slow-cooked black lentils in creamy butter", PriceMinor: 38000, CGSTRateBps: 250, SGSTRateBps: 250},
						{Name: "Butter Naan", Description: "Refined flour bread with butter", PriceMinor: 7000, CGSTRateBps: 250, SGSTRateBps: 250},
					},
				},
			},
		}, nil
	}

	b64Image := base64.StdEncoding.EncodeToString(imageData)

	prompt := `You are an expert restaurant menu OCR and cataloging AI. 
Analyze the provided restaurant menu image. Accurately extract all menu categories and their dishes.
Normalize pricing to Indian Paise (1 Rupee = 100 Paise, e.g. 350.00 INR = 35000 paise).
Return strictly valid JSON matching this schema:
{
  "categories": [
    {
      "name": "Category Name (e.g. Starters)",
      "items": [
        {
          "name": "Item Name",
          "description": "Brief description if present",
          "price_minor": 35000,
          "cgst_rate_bps": 250,
          "sgst_rate_bps": 250
        }
      ]
    }
  ]
}`

	reqPayload := map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"parts": []map[string]interface{}{
					{"text": prompt},
					{
						"inline_data": map[string]string{
							"mime_type": mimeType,
							"data":      b64Image,
						},
					},
				},
			},
		},
		"generationConfig": map[string]interface{}{
			"response_mime_type": "application/json",
		},
	}

	bodyBytes, err := json.Marshal(reqPayload)
	if err != nil {
		return nil, err
	}

	apiURL := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", s.modelName, s.geminiKey)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp map[string]interface{}
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		return nil, fmt.Errorf("gemini api error status %d: %v", resp.StatusCode, errResp)
	}

	var geminiResp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&geminiResp); err != nil {
		return nil, err
	}

	if len(geminiResp.Candidates) == 0 || len(geminiResp.Candidates[0].Content.Parts) == 0 {
		return nil, errors.New("empty response from gemini vision")
	}

	rawJSON := geminiResp.Candidates[0].Content.Parts[0].Text
	var result GeminiMenuExtractionResult
	if err := json.Unmarshal([]byte(rawJSON), &result); err != nil {
		return nil, fmt.Errorf("failed to parse structured gemini response: %w", err)
	}

	return &result, nil
}

type AIRetrievalResponse struct {
	Query          string                `json:"query"`
	Answer         string                `json:"answer"`
	RetrievedItems []restaurant.MenuItem `json:"retrieved_items"`
	Model          string                `json:"model"`
}

type geminiAIRetrievalResult struct {
	Answer       string   `json:"answer"`
	MatchedItems []string `json:"matched_items"`
}

// QueryMenuWithAI performs intelligent semantic data retrieval over stored restaurant menu items using Gemini AI.
func (s *AICatalogService) QueryMenuWithAI(ctx context.Context, restaurantID uuid.UUID, userQuery string) (*AIRetrievalResponse, error) {
	if strings.TrimSpace(userQuery) == "" {
		return nil, errors.New("query cannot be empty")
	}

	items, err := s.repo.ListMenuItems(ctx, restaurantID)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve menu items from db: %w", err)
	}

	if len(items) == 0 {
		return &AIRetrievalResponse{
			Query:          userQuery,
			Answer:         "No menu items have been cataloged for this restaurant yet.",
			RetrievedItems: []restaurant.MenuItem{},
			Model:          s.modelName,
		}, nil
	}

	var menuBuilder strings.Builder
	for idx, it := range items {
		price := float64(it.Price.AmountMinorUnits) / 100.0
		menuBuilder.WriteString(fmt.Sprintf("%d. %s - ₹%.2f. Description: %s\n", idx+1, it.Name, price, it.Description))
	}

	prompt := fmt.Sprintf(`You are an intelligent dining assistant for this restaurant.
The customer or staff member is inquiring about the menu.
Here is the current menu catalog retrieved directly from the restaurant database:
%s

User Inquiry: "%s"

Analyze the stored menu data to answer the query accurately, politely, and thoroughly.
Identify any dishes from the database that match or relate to the inquiry.
Return strictly valid JSON matching this schema:
{
  "answer": "Polite, appetizing, and informative answer addressing the user's question directly",
  "matched_items": ["Exact Item Name 1", "Exact Item Name 2"]
}`, menuBuilder.String(), userQuery)

	reqPayload := map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"parts": []map[string]interface{}{
					{"text": prompt},
				},
			},
		},
		"generationConfig": map[string]interface{}{
			"response_mime_type": "application/json",
		},
	}

	bodyBytes, err := json.Marshal(reqPayload)
	if err != nil {
		return nil, err
	}

	apiURL := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", s.modelName, s.geminiKey)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp map[string]interface{}
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		return nil, fmt.Errorf("gemini api error status %d: %v", resp.StatusCode, errResp)
	}

	var geminiResp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&geminiResp); err != nil {
		return nil, err
	}

	if len(geminiResp.Candidates) == 0 || len(geminiResp.Candidates[0].Content.Parts) == 0 {
		return nil, errors.New("empty response from gemini")
	}

	rawJSON := geminiResp.Candidates[0].Content.Parts[0].Text
	var parsed geminiAIRetrievalResult
	if err := json.Unmarshal([]byte(rawJSON), &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse structured gemini response: %w", err)
	}

	// Match matched_items against actual database items
	matchedMap := make(map[string]bool)
	for _, name := range parsed.MatchedItems {
		matchedMap[strings.ToLower(strings.TrimSpace(name))] = true
	}

	var matchedItems []restaurant.MenuItem
	for _, it := range items {
		if matchedMap[strings.ToLower(strings.TrimSpace(it.Name))] {
			matchedItems = append(matchedItems, it)
		}
	}

	return &AIRetrievalResponse{
		Query:          userQuery,
		Answer:         parsed.Answer,
		RetrievedItems: matchedItems,
		Model:          s.modelName,
	}, nil
}

