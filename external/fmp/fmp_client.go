package fmp

import (
	"context"
	"encoding/json"
	"fmt"
	external "nexbit/external"
	models "nexbit/models"
	util "nexbit/util"
)

const API_TOKEN = "xpL651iwSgtcDTAYp6iCHsTL0NjmTEfg"
const BASE_URL = "https://financialmodelingprep.com/api/v3/"

type FmpApiClient struct {
	httpClient *external.HTTPClient
}

func NewAPIClient(httpClient *external.HTTPClient) *FmpApiClient {
	return &FmpApiClient{
		httpClient: httpClient,
	}
}

func (c *FmpApiClient) FetchIncomeStatementAPI(ctx context.Context, stockSymbol string, duration string) ([]*models.IncomeStatementResponse, error) {
	url := fmt.Sprintf("%sincome-statement/%s?period=%s&apikey=%s", BASE_URL, stockSymbol, duration, API_TOKEN)
	data, err := c.httpClient.Get(ctx, url, nil)
	var resp []*models.IncomeStatementResponse
	if err != nil {
		return nil, nil
	}

	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON response: %w", err)
	}

	return resp, nil
}

func (c *FmpApiClient) FetchCashFlowStatement(ctx context.Context, stockSymbol string, duration string) ([]*models.CashFlowResponse, error) {
	url := fmt.Sprintf("%scash-flow-statement/%s?period=%s&apikey=%s", BASE_URL, stockSymbol, duration, API_TOKEN)
	data, err := c.httpClient.Get(ctx, url, nil)
	var resp []*models.CashFlowResponse
	if err != nil {
		return nil, nil
	}

	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON response: %w", err)
	}

	return resp, nil
}

func (c *FmpApiClient) FetchBalanceSheet(ctx context.Context, stockSymbol string, duration string) ([]*models.BalanceSheetResponse, error) {
	url := fmt.Sprintf("%sbalance-sheet-statement/%s?period=%s&apikey=%s", BASE_URL, stockSymbol, duration, API_TOKEN)
	data, err := c.httpClient.Get(ctx, url, nil)
	var resp []*models.BalanceSheetResponse
	if err != nil {
		return nil, nil
	}

	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON response: %w", err)
	}

	return resp, nil
}

func (c *FmpApiClient) FetchFinancialsRatio(ctx context.Context, stockSymbol string, duration string) ([]*models.FinancialRatiosResponse, error) {
	url := fmt.Sprintf("%sratios/%s?period=%s&apikey=%s", BASE_URL, stockSymbol, duration, API_TOKEN)
	data, err := c.httpClient.Get(ctx, url, nil)
	var resp []*models.FinancialRatiosResponse
	if err != nil {
		return nil, nil
	}

	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON response: %w", err)
	}

	for _, item := range resp {
		util.RoundFloatFields(item)
	}

	return resp, nil
}

func (c *FmpApiClient) StockPrice(ctx context.Context, stockSymbol string) ([]*models.StockPriceResponse, error) {
	url := fmt.Sprintf("%shistorical-price-full/%s?apikey=%s", BASE_URL, stockSymbol, API_TOKEN)
	data, err := c.httpClient.Get(ctx, url, nil)
	var resp []models.StockPriceResponse
	if err != nil {
		return nil, nil
	}

	if err := json.Unmarshal(data, &resp); err != nil {
		fmt.Println(err)
		fmt.Println("here")
		return nil, fmt.Errorf("failed to unmarshal JSON response: %w", err)
	}

	return nil, nil
}
