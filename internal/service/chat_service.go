package service

import (
	"context"
	"encoding/json"
	"fmt"
	fmpApiClient "nexbit/external/fmp"
	newsApiClient "nexbit/external/news"
	openAiClient "nexbit/external/openai"
	"nexbit/internal/repo"
	"nexbit/util"
	"path/filepath"
	"strings"
	"time"

	models "nexbit/models"

	"github.com/gofiber/fiber/v2"
	"github.com/sashabaranov/go-openai"
)

type ChatService struct {
	openAiClient  *openAiClient.OpenAiClient
	fmpApiClient  *fmpApiClient.FmpApiClient
	newsApiClient *newsApiClient.NewsApiClient
	db            *repo.DBService
}

func NewChatService(db *repo.DBService, openAiClient *openAiClient.OpenAiClient, fmpApiClient *fmpApiClient.FmpApiClient, newsApiClient *newsApiClient.NewsApiClient) *ChatService {
	return &ChatService{
		openAiClient:  openAiClient,
		fmpApiClient:  fmpApiClient,
		newsApiClient: newsApiClient,
		db:            db,
	}
}

func (s *ChatService) ChatService(ctx *fiber.Ctx) error {
	messages := ctx.Locals("requestData").(models.SubmitChatRequest)
	var chatgptMessages []openai.ChatCompletionMessage

	for _, message := range messages.Message {
		var chatgptMessage openai.ChatCompletionMessage
		chatgptMessage.Role = message.Role
		chatgptMessage.Content = message.Content
		chatgptMessages = append(chatgptMessages, chatgptMessage)
	}

	chatResponse, err := s.openAiClient.ChatCompletionClient(ctx.Context(), chatgptMessages)
	if err != nil {
		util.WithContext(ctx.Context()).Errorf("[ChatService] Failed to process chat request. err: %v", err)
		return err
	}

	return ctx.JSON(fiber.Map{
		"message": chatResponse.Choices[0].Message,
	})
}

func (s *ChatService) FetchFundamentals(ctx *fiber.Ctx, stockSymbol string) (*models.FundamentalDataResponse, error) {
	incomeStatementResponse, err := s.fmpApiClient.FetchIncomeStatementAPI(ctx.Context(), stockSymbol, "annual")
	if err != nil {
		util.WithContext(ctx.Context()).Errorf("[ChatService] Failed to process chat request. err: %v", err)
		return nil, err
	}

	balanceSheetResponse, err := s.fmpApiClient.FetchBalanceSheet(ctx.Context(), stockSymbol, "annual")
	if err != nil {
		util.WithContext(ctx.Context()).Errorf("[ChatService] Failed to process chat request. err: %v", err)
		return nil, err
	}

	cashFlowResponse, err := s.fmpApiClient.FetchCashFlowStatement(ctx.Context(), stockSymbol, "annual")
	if err != nil {
		util.WithContext(ctx.Context()).Errorf("[ChatService] Failed to process chat request. err: %v", err)
		return nil, err
	}

	financialRationResponse, err := s.fmpApiClient.FetchFinancialsRatio(ctx.Context(), stockSymbol, "annual")
	if err != nil {
		util.WithContext(ctx.Context()).Errorf("[ChatService] Failed to process chat request. err: %v", err)
		return nil, err
	}

	finalRespnse := &models.FundamentalDataResponse{
		BalanceSheetResponse:    balanceSheetResponse,
		IncomeStatementResponse: incomeStatementResponse,
		CashFlowResponse:        cashFlowResponse,
		FinancialRatiosResponse: financialRationResponse,
	}

	return finalRespnse, nil
}

func (s *ChatService) FetchNewsInsights(ctx *fiber.Ctx) error {
	stockSymbol := ctx.Locals("stockSymbol").(string)

	insights, err := s.newsApiClient.FetchNewsInsights(ctx.Context(), stockSymbol)
	if err != nil {
		util.WithContext(ctx.Context()).Errorf("[ChatService] Failed to process chat request. err: %v", err)
		return err
	}

	return ctx.JSON(insights)
}

func (s *ChatService) Uploadfile(ctx *fiber.Ctx, req models.FileUploadRequest) error {
	fileInfo, err := getFileInfos(req.FilePaths)
	if err != nil {
		util.WithContext(ctx.Context()).Errorf("[Uploadfile] Failed to get file infos. err: %v", err)
		return err
	}

	var fileIDs []string
	for _, info := range fileInfo {
		fileReq := openai.FileRequest{
			FileName: info.Name,
			FilePath: info.Path,
			Purpose:  "assistants",
		}

		file, err := s.openAiClient.UploadFileClient(ctx.Context(), fileReq)
		if err != nil {
			util.WithContext(ctx.Context()).Errorf("[Uploadfile] Failed to upload file: %s. err: %v", info.Name, err)
			return err
		}
		fileIDs = append(fileIDs, file.ID)
	}

	time.Sleep(10 * time.Second)

	// Prepare the chat message with content generated by the separate method
	chatgptMessages := []openai.ChatCompletionMessage{
		{
			Role:    "user",
			Content: s.generatePromptContent(fileIDs),
		},
	}

	chatResponse, err := s.openAiClient.ChatCompletionClient(ctx.Context(), chatgptMessages)
	if err != nil {
		util.WithContext(ctx.Context()).Errorf("[Uploadfile] Failed to process chat request. err: %v", err)
		return err
	}

	var response models.StockResearchResponse
	if err := json.Unmarshal([]byte(chatResponse.Choices[0].Message.Content), &response); err != nil {
		return fmt.Errorf("[Uploadfile] Error parsing JSON: %v", err)
	}

	if response.Err != "" {
		return fmt.Errorf("[Uploadfile] Tailored error response from gpt: %v", err)
	}

	for _, reportResponse := range response.Data {

		dbReq := repo.StockResearchReport{
			Company:        reportResponse.Company,
			Sector:         reportResponse.Sector,
			Recommendation: reportResponse.Recommendation,
			TargetPrice:    reportResponse.TargetPrice,
			NewsSummary:    reportResponse.NewsSummary,
		}

		err := s.db.SaveStockReport(context.Background(), dbReq)
		if err != nil {
			util.WithContext(ctx.Context()).Errorf("[Uploadfile] Failed to save data in database. err: %v", err)
			return err
		}

	}

	return nil
}

func (s *ChatService) UserQueryService(ctx *fiber.Ctx, messages models.SubmitChatRequest) (*openai.ChatCompletionMessage, error) {
	var chatgptMessages []openai.ChatCompletionMessage
	var chatgptMessage openai.ChatCompletionMessage

	latestMessage := messages.Message[len(messages.Message)-1].Content

	for index, message := range messages.Message {
		if index == len(messages.Message)-1 {
			continue
		}
		var chatgptMessage openai.ChatCompletionMessage
		chatgptMessage.Role = message.Role
		chatgptMessage.Content = message.Content

		chatgptMessages = append(chatgptMessages, chatgptMessage)
	}

	userQuery, cont, _ := s.parseUserQuery(ctx, latestMessage)
	if !cont {
		chatgptMessage.Role = "user"
		chatgptMessage.Content = userQuery
		return &chatgptMessage, nil
	}

	var userQueryMessage openai.ChatCompletionMessage
	userQueryMessage.Role = "user"
	userQueryMessage.Content = userQuery

	chatgptMessages = append(chatgptMessages, userQueryMessage)

	chatResponse, err := s.openAiClient.ChatCompletionClient(ctx.Context(), chatgptMessages)
	if err != nil {
		util.WithContext(ctx.Context()).Errorf("[UserQueryService] Failed to process chat request. err: %v", err)
		return nil, err
	}

	return &chatResponse.Choices[0].Message, nil
}

func (s *ChatService) parseUserQuery(ctx *fiber.Ctx, userQuery string) (string, bool, error) {
	fetchQueryObject, err := s.fetchParseUserQuery(ctx, userQuery)
	if err != nil {
		util.WithContext(ctx.Context()).Errorf("[parseUserQuery] Failed to process user query request. err: %v", err)
		return "", false, err
	}

	if fetchQueryObject.Error != nil {
		util.WithContext(ctx.Context()).Errorf("[parseUserQuery] got custom error while parsing user query. err: %v", err)
		return "", false, err
	}

	fmt.Println(fetchQueryObject.Data)

	switch fetchQueryObject.Data.Intent {
	case util.BUY:
		return s.buyIntentFlow(ctx, fetchQueryObject.Data, userQuery), true, nil
	case util.SELL:
		return s.sellIntentFlow(ctx, fetchQueryObject.Data, userQuery), true, nil
	case util.RESEARCH:
		return s.researchIntentFlow(ctx, fetchQueryObject.Data, userQuery), true, nil
	case util.OTHER:
		return s.otherNotRelevantIntentFlow(ctx), false, nil
	default:
		util.WithContext(ctx.Context()).Errorf("[parseUserQuery] user query in invalid. err: %v", err)
		return "", false, err
	}
}

func (s *ChatService) fetchParseUserQuery(ctx *fiber.Ctx, userQuery string) (models.UserParseQueryResponse, error) {
	chatgptMessage := []openai.ChatCompletionMessage{
		{
			Role:    "user",
			Content: s.generateUserQueryParsePrompt(userQuery),
		},
	}

	var response models.UserParseQueryResponse
	chatResponse, err := s.openAiClient.ChatCompletionClient(ctx.Context(), chatgptMessage)
	if err != nil {
		util.WithContext(ctx.Context()).Errorf("[fetchParseUserQuery] Failed to process chat request. err: %v", err)
		return response, err
	}

	cleanedContent := strings.TrimSpace(chatResponse.Choices[0].Message.Content)
	if strings.HasPrefix(cleanedContent, "```json") {
		cleanedContent = strings.TrimPrefix(cleanedContent, "```json")
	}
	if strings.HasPrefix(cleanedContent, "```") {
		cleanedContent = strings.TrimPrefix(cleanedContent, "```")
	}
	if strings.HasSuffix(cleanedContent, "```") {
		cleanedContent = strings.TrimSuffix(cleanedContent, "```")
	}

	fmt.Println(cleanedContent)
	err = json.Unmarshal([]byte(cleanedContent), &response)
	if err != nil {
		return response, fmt.Errorf("[fetchParseUserQuery] failed to parse res with err %v", err)
	}
	return response, nil
}

func (s *ChatService) generateUserQueryParsePrompt(userQuery string) string {
	return fmt.Sprintf("Given the user query, extract the following information:\n"+
		"- intent (BUY, SELL, RESEARCH, OTHER); OTHER if unrelated to stock market, finance, investment,stock market knowledge or wealth;\n"+
		"- ticker (Nasdaq stock ticker, if present)\n"+
		"- company name (the name of the company associated with the ticker, if present)\n"+
		"- amount (any mentioned amount)\n"+
		"- sector (any sector information, if present)\n"+
		"- horizon (time frame, if mentioned)\n"+
		"- news (if any news is referenced)\n"+
		"- info_type (only if intent is RESEARCH; indicate the data types needed: 'income_statement', 'cashflow_statement', 'financial_ratios','balance_sheet', 'news')\n"+
		"Respond in the following JSON format:\n"+
		"{\n"+
		"  \"data\": {\n"+
		"    \"intent\": \"\",\n"+
		"    \"ticker\": \"\",\n"+
		"    \"company_name\": \"\",\n"+
		"    \"amount\": \"\",\n"+
		"    \"sector\": \"\",\n"+
		"    \"horizon\": \"\",\n"+
		"    \"info_type\": \"\",\n"+
		"    \"news\": \"\"\n"+
		"  },\n"+
		"  \"error\": null\n"+
		"}\n\n"+
		"User query: \"%s\"", userQuery)

}

func (s *ChatService) generateMainPromptForUserQuery(userQuery string) string {

	currentTime := time.Now().Format("2006-01-02 15:04:05")
	prompt := `You are an investment research Analyst. Based on the provided data, answer the user query as directly and concisely as possible.
 Use no more than 70 words and avoid unnecessary financial jargon. Highlight relevant figures only if they directly answer the query
 Guidelines:
1. Address only the information requested in the user query (e.g., revenue, profit, market trends).
2. Avoid extra commentary unless specified by the user query.
3. If historical data is included, summarize trends briefly if they provide context to the answer.
4. Avoid recommendations unless directly asked.`
	return fmt.Sprintf("for context today's date and time is %s. The user question is:%s, now %s", currentTime, userQuery, prompt)
}

func (s *ChatService) generatePromptContent(fileIDs []string) string {
	return fmt.Sprintf("You have been provided with a list of file IDs corresponding to stock research reports. Each research report contains detailed information about the performance of Indian publicly listed companies. Your task is to analyze each file thoroughly and extract the relevant data to populate a StockReport object. Please adhere strictly to the following instructions:\n\n1. Extract only verified and factual information directly from the reports—do not infer or assume any details.\n2. For each report, fill out the following StockReport struct:\n   \n    type StockReport struct {\n        Company            string    `json:\"company\"`           // Name of the company (usually in the title or company info section)\n        Sector             string    `json:\"sector\"`            // Industry sector (found near the company name or at the top of the report)\n        Recommendation     string    `json:\"recommendation\"`    // Analyst recommendation (e.g., Buy, Hold, Sell, often in the first few pages)\n        TargetPrice        float64   `json:\"target_price\"`      // Target price in INR (commonly near the recommendation)\n        RevenueProjections []float64 `json:\"revenue_projections\"` // Revenue projections for future years (usually found in financial projections section)\n        CAGR               float64   `json:\"cagr\"`              // Compound Annual Growth Rate (CAGR, found in financial projections)\n        EBITDA             float64   `json:\"ebitda\"`            // Earnings before Interest, Taxes, Depreciation, and Amortization (financial section)\n        NewsSummary        string    `json:\"news_summary\"`      // Summary of key news related to the company (usually in a company update or news section)\n    }\n    \n3. **Guidance on extraction**:\n    - The **company name** and **sector** are typically found in the title or introductory section of the report.\n    - The **recommendation** and **target price** are located on the first or second page under a headline such as \"BUY,\" \"SELL,\" or \"HOLD.\"\n    - **Revenue projections** are located in the financial tables under \"Net Sales\" or \"Revenue,\" often alongside future fiscal years (FY24, FY25E, FY26E).\n    - **CAGR** and **EBITDA** values are found in the financial projections section, typically as percentages or absolute values.\n    - **News summary** is extracted from the narrative sections of the report, detailing company updates or significant events.\n\n4. Make sure to cross-check each extracted field for accuracy. If data is missing for any field, mark it as `null` or \"N/A\" in the JSON output.\n\n5. The final output must be formatted as JSON, without any additional explanations or assumptions. Here is the required JSON format:\n\n    {\n        \"data\": [ \n            { \"company\": \"string\", \"sector\": \"string\", \"recommendation\": \"string\", \"target_price\": \"float64\", \"revenue_projections\": [ \"float64\" ], \"cagr\": \"float64\", \"ebitda\": \"float64\", \"news_summary\": \"string\" }\n        ],\n        \"err\": null if successful, or a relevant error message if extraction fails.\n    }\n\n6. **IMPORTANT**: Only process the provided file IDs: %v", fileIDs)
}

func getFileInfos(filePaths []string) ([]models.FileInfo, error) {
	var fileInfos []models.FileInfo

	for _, filePath := range filePaths {
		fileName := filepath.Base(filePath)
		fileInfos = append(fileInfos, models.FileInfo{Name: fileName, Path: filePath})
	}

	return fileInfos, nil
}

func (s *ChatService) buyIntentFlow(ctx *fiber.Ctx, userParseQuery models.UserParseQuery, userQuery string) string {
	var promptBuilder strings.Builder

	promptBuilder.WriteString(s.generateMainPromptForUserQuery(userQuery))

	if userParseQuery.Ticker != "" {

		//fetch fundamentals
		// fundamentalData, err := s.buildFundamentaWithPrompt(ctx, userParseQuery.Ticker)
		// if err != nil {
		// 	return ""
		// }
		// promptBuilder.WriteString(fundamentalData)
		//FetchNewsInsights
		newsData, err := s.buildNewsWithPrompt(ctx, userParseQuery.Ticker)
		if err != nil {
			return ""
		}
		promptBuilder.WriteString(newsData)

		fmt.Println(promptBuilder.String())
	}

	fmt.Println(promptBuilder.String())
	//sector has to add
	return promptBuilder.String()
}
func (s *ChatService) sellIntentFlow(ctx *fiber.Ctx, userParseQuery models.UserParseQuery, userQuery string) string {
	var promptBuilder strings.Builder
	promptBuilder.WriteString(s.generateMainPromptForUserQuery(userQuery))
	if userParseQuery.Ticker != "" {

		//fetch fundamentals
		// fundamentalData, err := s.buildFundamentaWithPrompt(ctx, userParseQuery.Ticker)
		// if err != nil {
		// 	return ""
		// }
		// promptBuilder.WriteString(fundamentalData)
		//FetchNewsInsights
		newsData, err := s.buildNewsWithPrompt(ctx, userParseQuery.Ticker)
		if err != nil {
			return ""
		}
		promptBuilder.WriteString(newsData)
	}

	//sector has to add
	return promptBuilder.String()
}

func (s *ChatService) researchIntentFlow(ctx *fiber.Ctx, userParseQuery models.UserParseQuery, userQuery string) string {

	var promptBuilder strings.Builder
	promptBuilder.WriteString(s.generateMainPromptForUserQuery(userQuery))
	if userParseQuery.Ticker != "" {

		//fetch fundamentals

		if strings.Contains(userParseQuery.InfoType, "income_statement") {
			fundamentalData, err := s.buildFundamentaWithPrompt(ctx, userParseQuery.Ticker, "income_statement")
			if err != nil {
				return ""
			}
			promptBuilder.WriteString(fundamentalData)
		}

		if strings.Contains(userParseQuery.InfoType, "cashflow_statement") {
			fundamentalData, err := s.buildFundamentaWithPrompt(ctx, userParseQuery.Ticker, "cashflow_statement")
			if err != nil {
				return ""
			}
			promptBuilder.WriteString(fundamentalData)
		}

		if strings.Contains(userParseQuery.InfoType, "financial_ratios") {
			fundamentalData, err := s.buildFundamentaWithPrompt(ctx, userParseQuery.Ticker, "financial_ratios")
			if err != nil {
				return ""
			}
			promptBuilder.WriteString(fundamentalData)
		}

		if strings.Contains(userParseQuery.InfoType, "balance_sheet") {
			fundamentalData, err := s.buildFundamentaWithPrompt(ctx, userParseQuery.Ticker, "balance_sheet")
			if err != nil {
				return ""
			}
			promptBuilder.WriteString(fundamentalData)
		}

		if strings.Contains(userParseQuery.InfoType, "news") {
			newsData, err := s.buildNewsWithPrompt(ctx, userParseQuery.Ticker)
			if err != nil {
				return ""
			}
			promptBuilder.WriteString(newsData)
		}

		if strings.Contains(userParseQuery.InfoType, "stock_report") {
			reports, err := s.buildResearchReportsWithPrompt(ctx, userParseQuery)
			if err != nil {
				return ""
			}

			promptBuilder.WriteString(reports)
		}
	}

	fmt.Println(promptBuilder.String())
	//sector has to add
	return promptBuilder.String()
}

func (s *ChatService) otherNotRelevantIntentFlow(ctx *fiber.Ctx) string {

	prompt := fmt.Sprintf("this chatbot is build for investment and finance related queries only")

	//sector has to add
	return prompt
}

func (s *ChatService) buildFundamentaWithPrompt(ctx *fiber.Ctx, ticker string, fundamentalType string) (string, error) {

	switch fundamentalType {
	case "income_statement":
		incomeStatementResponse, err := s.fmpApiClient.FetchIncomeStatementAPI(ctx.Context(), ticker, "annual")
		if err != nil {
			util.WithContext(ctx.Context()).Errorf("[ChatService] Failed to process chat request. err: %v", err)
			return "", err
		}

		fundamentalJsonData, err := json.Marshal(incomeStatementResponse)

		prompt := fmt.Sprintf("Here’s the income statement data .\n\nData: %s", fundamentalJsonData)

		return prompt, nil

	case "balance_sheet":
		balanceSheetResponse, err := s.fmpApiClient.FetchBalanceSheet(ctx.Context(), ticker, "annual")
		if err != nil {
			util.WithContext(ctx.Context()).Errorf("[ChatService] Failed to process chat request. err: %v", err)
			return "", err
		}

		balanceSheetJsonData, err := json.Marshal(balanceSheetResponse)

		prompt := fmt.Sprintf("Here’s the balance sheet data .\n\nData: %s", balanceSheetJsonData)

		return prompt, nil

	case "cashflow_statement":
		cashFlowResponse, err := s.fmpApiClient.FetchCashFlowStatement(ctx.Context(), ticker, "annual")
		if err != nil {
			util.WithContext(ctx.Context()).Errorf("[ChatService] Failed to process chat request. err: %v", err)
			return "", err
		}
		cashFlowJsonData, err := json.Marshal(cashFlowResponse)

		prompt := fmt.Sprintf("Here’s the cash flow statement data .\n\nData: %s", cashFlowJsonData)

		return prompt, nil

	case "financial_ratios":
		financialRatioResponse, err := s.fmpApiClient.FetchFinancialsRatio(ctx.Context(), ticker, "annual")
		if err != nil {
			util.WithContext(ctx.Context()).Errorf("[ChatService] Failed to process chat request. err: %v", err)
			return "", err
		}

		financialRatioJsonData, err := json.Marshal(financialRatioResponse)

		prompt := fmt.Sprintf("Here’s the financial ratio data .\n\nData: %s", financialRatioJsonData)

		return prompt, nil
	}

	return "none", nil
}

func (s *ChatService) buildNewsWithPrompt(ctx *fiber.Ctx, ticker string) (string, error) {

	insights, err := s.newsApiClient.FetchNewsInsights(ctx.Context(), ticker)
	if err != nil {
		util.WithContext(ctx.Context()).Errorf("[buildNewsWithPrompt] Failed to process fetch news request. err: %v", err)
		return "", err
	}

	fundamentalJsonData, err := json.Marshal(insights)
	if err != nil {
		return "", err
	}

	prompt := fmt.Sprintf("Here’s the latest news summary related to %s. Analyze the news and explain how it might impact the company’s stock performance or the broader market.\n\nNews Summary: %s", ticker, string(fundamentalJsonData))
	return prompt, nil
}

func (s *ChatService) buildResearchReportsWithPrompt(ctx *fiber.Ctx, userParsedQuery models.UserParseQuery) (string, error) {
	dbReq := repo.StockResearchFetchRequest{
		Date:        "28/08/2024",
		Sector:      userParsedQuery.Sector,
		CompanyName: userParsedQuery.CompanyName,
		Ticker:      userParsedQuery.Ticker,
	}

	resp, err := s.db.FetchStockReport(context.Background(), dbReq)
	if err != nil {
		util.WithContext(ctx.Context()).Errorf("[fetchStockResearchReports] Failed to fetch reports from database. err: %v", err)
		return "", err
	}

	reportsJsonData, err := json.Marshal(resp)
	if err != nil {
		return "", err
	}
	prompt := fmt.Sprintf("Here’s a summary of the stock research reports in JSON format: %s. Analyze these reports and identify any key insights, trends, or risks related to the companies’ performances. Highlight areas of growth, stability, or concern.", reportsJsonData)
	return prompt, nil
}
