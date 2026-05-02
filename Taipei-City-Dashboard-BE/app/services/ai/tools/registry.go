package tools

import (
	"TaipeiCityDashboardBE/app/models"
	"TaipeiCityDashboardBE/app/services"
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// ToolFunc defines the signature for a tool function
type ToolFunc func(ctx context.Context, args string) (string, error)

var registry = make(map[string]ToolFunc)

func init() {
	Register("get_current_time", GetCurrentTime)
	Register("search_dashboards", SearchDashboardsTool)
	Register("get_component_data", GetComponentDataTool)
	Register("query_city_data", QueryCityDataTool)
}

// Register adds a tool to the registry
func Register(name string, fn ToolFunc) {
	registry[name] = fn
}

// Execute calls a registered tool with the given arguments
func Execute(ctx context.Context, name string, args string) (string, error) {
	fn, ok := registry[name]
	if !ok {
		return "", fmt.Errorf("tool %s not found", name)
	}
	return fn(ctx, args)
}

// GetCurrentTime is a demo tool that returns the current Taipei time
func GetCurrentTime(ctx context.Context, args string) (string, error) {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		return time.Now().Format(time.RFC3339), nil
	}
	return time.Now().In(loc).Format("2006-01-02 15:04:05"), nil
}

func SearchDashboardsTool(ctx context.Context, args string) (string, error) {
	var params map[string]string
	if err := parseArgs(args, &params); err != nil {
		return "", fmt.Errorf("參數解析失敗: %v", err)
	}
	query := params["query"]

	if query == "" {
		return "[]", nil
	}

	results, err := services.SearchQdrantComponents(ctx, query, 10, 0.8)
	if err != nil {
		return "", fmt.Errorf("向量搜尋失敗: %v", err)
	}

	if len(results) == 0 {
		return "[]", nil
	}

	resultBytes, _ := json.Marshal(results)
	return string(resultBytes), nil
}

// GetComponentDataTool 依組件 index 從資料庫取得實際數據
func GetComponentDataTool(ctx context.Context, args string) (string, error) {
	var params struct {
		Index    string `json:"index"`
		City     string `json:"city"`
		TimeFrom string `json:"time_from"`
		TimeTo   string `json:"time_to"`
	}
	if err := parseArgs(args, &params); err != nil {
		return "", fmt.Errorf("參數解析失敗: %v", err)
	}
	if params.Index == "" {
		return "", fmt.Errorf("index 不可為空")
	}
	if params.City == "" {
		params.City = "taipei"
	}

	// 預設時間範圍：最近 24 小時
	loc, _ := time.LoadLocation("Asia/Taipei")
	if params.TimeTo == "" {
		params.TimeTo = time.Now().In(loc).Format("2006-01-02T15:04:05+08:00")
	}
	if params.TimeFrom == "" {
		params.TimeFrom = time.Now().In(loc).Add(-24 * time.Hour).Format("2006-01-02T15:04:05+08:00")
	}

	queryType, queryString, err := models.GetComponentChartDataByIndex(params.Index, params.City)
	if err != nil {
		return "", fmt.Errorf("查詢組件設定失敗: %v", err)
	}
	if queryString == "" {
		return fmt.Sprintf("組件 %s 在 %s 目前沒有可用資料", params.Index, params.City), nil
	}

	var result interface{}
	switch queryType {
	case "two_d":
		data, err := models.GetTwoDimensionalData(&queryString, params.TimeFrom, params.TimeTo)
		if err != nil {
			return "", fmt.Errorf("取得 2D 資料失敗: %v", err)
		}
		result = data
	case "three_d", "percent":
		data, categories, err := models.GetThreeDimensionalData(&queryString, params.TimeFrom, params.TimeTo)
		if err != nil {
			return "", fmt.Errorf("取得 3D 資料失敗: %v", err)
		}
		result = map[string]interface{}{"data": data, "categories": categories}
	case "time":
		data, err := models.GetTimeSeriesData(&queryString, params.TimeFrom, params.TimeTo)
		if err != nil {
			return "", fmt.Errorf("取得時序資料失敗: %v", err)
		}
		result = data
	case "map_legend":
		data, err := models.GetMapLegendData(&queryString, params.TimeFrom, params.TimeTo)
		if err != nil {
			return "", fmt.Errorf("取得地圖資料失敗: %v", err)
		}
		result = data
	default:
		return fmt.Sprintf("不支援的資料類型: %s", queryType), nil
	}

	resultBytes, _ := json.Marshal(result)
	return string(resultBytes), nil
}

// QueryCityDataTool 依 index 直接取得實際數據（AI 從組件清單選定 index 後呼叫）
// 若未提供 index，則退而使用向量搜尋
func QueryCityDataTool(ctx context.Context, args string) (string, error) {
	var params struct {
		Index    string `json:"index"`
		Query    string `json:"query"`
		City     string `json:"city"`
		TimeFrom string `json:"time_from"`
		TimeTo   string `json:"time_to"`
	}
	if err := parseArgs(args, &params); err != nil {
		return "", fmt.Errorf("參數解析失敗: %v", err)
	}
	if params.City == "" {
		params.City = "taipei"
	}

	loc, _ := time.LoadLocation("Asia/Taipei")
	if params.TimeTo == "" {
		params.TimeTo = time.Now().In(loc).Format("2006-01-02T15:04:05+08:00")
	}
	if params.TimeFrom == "" {
		params.TimeFrom = time.Now().In(loc).Add(-24 * time.Hour).Format("2006-01-02T15:04:05+08:00")
	}

	targetIndex := params.Index
	componentName := ""

	// index 未提供時，退而使用向量搜尋
	if targetIndex == "" {
		if params.Query == "" {
			return "", fmt.Errorf("index 或 query 至少需要提供一個")
		}
		components, err := services.SearchQdrantComponents(ctx, params.Query, 3, 0.8)
		if err != nil || len(components) == 0 {
			return "找不到與查詢相關的組件，請確認組件清單是否有此主題", nil
		}
		targetIndex = components[0].Index
		componentName = components[0].Name
		if params.City == "taipei" && components[0].City != "" {
			params.City = components[0].City
		}
	}

	// 取 query_chart SQL 與單位
	info, err := models.GetComponentQueryInfoByIndex(targetIndex, params.City)
	if err != nil {
		return "", fmt.Errorf("查詢組件設定失敗: %v", err)
	}
	if info.QueryChart == "" {
		return fmt.Sprintf("組件 %s 在 %s 目前沒有可用資料", targetIndex, params.City), nil
	}

	var chartData interface{}
	switch info.QueryType {
	case "two_d":
		chartData, err = models.GetTwoDimensionalData(&info.QueryChart, params.TimeFrom, params.TimeTo)
	case "three_d", "percent":
		var categories []string
		var seriesData []models.ThreeDimensionalDataOutput
		seriesData, categories, err = models.GetThreeDimensionalData(&info.QueryChart, params.TimeFrom, params.TimeTo)

		// 預先計算各 category（x_axis，如行政區）的所有 series 加總
		// 避免 AI 只取單一格數值而非加總後的總量
		type categoryTotal struct {
			Category string `json:"category"`
			Total    int    `json:"total"`
		}
		totals := make([]categoryTotal, 0, len(categories))
		for j, cat := range categories {
			total := 0
			for _, series := range seriesData {
				if j < len(series.Data) {
					total += series.Data[j]
				}
			}
			totals = append(totals, categoryTotal{Category: cat, Total: total})
		}
		chartData = map[string]interface{}{
			"data":            seriesData,
			"categories":      categories,
			"category_totals": totals,
		}
	case "time":
		chartData, err = models.GetTimeSeriesData(&info.QueryChart, params.TimeFrom, params.TimeTo)
	case "map_legend":
		chartData, err = models.GetMapLegendData(&info.QueryChart, params.TimeFrom, params.TimeTo)
	default:
		return fmt.Sprintf("不支援的資料類型: %s", info.QueryType), nil
	}
	if err != nil {
		return "", fmt.Errorf("取得資料失敗: %v", err)
	}

	// 將資料序列化為 JSON 字串以便格式化
	dataBytes, _ := json.Marshal(chartData)
	dataStr := string(dataBytes)
	if dataStr == "null" || dataStr == "[]" || dataStr == "{}" || dataStr == `[{}]` {
		return fmt.Sprintf(
			"【資料庫查詢結果】組件：%s（%s）\n查詢時間：%s ~ %s\n結果：查無資料，請直接告知用戶此時間範圍內沒有資料，不可補充任何數值。",
			targetIndex, params.City, params.TimeFrom, params.TimeTo,
		), nil
	}

	unit := info.Unit
	if unit == "" {
		unit = "（無單位）"
	}
	name := componentName
	if name == "" {
		name = targetIndex
	}

	// 依資料型別加入解讀提示，避免 AI 誤讀矩陣結構
	var dataHint string
	switch info.QueryType {
	case "three_d", "percent":
		dataHint = "\n資料結構說明：此為三維圖資料，data 是多個 series 的矩陣，categories 為 X 軸標籤（如年齡層）。" +
			"【重要】資料中已附上 category_totals 欄位，這是各 category（如行政區）所有 series 加總後的結果。" +
			"若要比較哪個行政區（或類別）總量最大，必須使用 category_totals 欄位，找出 total 最大的項目，" +
			"不可直接取 data 中的單一數值作為總量。單位（unit）適用於 category_totals 中的每個 total 值，回答時必須附上單位。"
	case "two_d":
		dataHint = "\n資料結構說明：此為一維陣列，每筆資料含 x（類別）與 y（數值）欄位，單位為：" + unit + "。回答時必須附上單位。"
	case "time":
		dataHint = "\n資料結構說明：此為時間序列，每筆資料含時間戳與數值，單位為：" + unit + "。回答時必須附上單位。"
	}

	return fmt.Sprintf(
		"【資料庫查詢結果 - 僅能使用以下數值回答，嚴禁引用訓練知識】\n組件：%s（%s）\n單位：%s（回答時必須附上此單位）\n查詢時間：%s ~ %s%s\n數據：%s",
		name, params.City, unit, params.TimeFrom, params.TimeTo, dataHint, dataStr,
	), nil
}

func parseArgs(args string, v interface{}) error {
	return json.Unmarshal([]byte(args), v)
}
