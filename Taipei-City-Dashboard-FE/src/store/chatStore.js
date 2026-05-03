import { ref, watch } from "vue";
import { defineStore } from "pinia";
import http from "../router/axios";

export const useChatStore = defineStore("chat", () => {
	// 預設訊息
	const defaultChatData = [
		{
			id: 1,
			role: "bot",
			isDefault: true,
			content:
				"您好，我是【臺北城市儀表板】小幫手，很高興為您服務！\n 您可以： \n\n • 點擊左側既有的儀表板主題，快速查看各主題內容 \n • 輸入您感興趣的主題描述，我會自動為您組建最適合的儀表板 \n\n 如果有想了解的內容，歡迎直接告訴我，我會盡力協助！\n\n 📩 聯絡信箱：tuic@gov.taipei \n 🏢 臺北大數據中心 \n\n",
		},
	];

	const recommendComponents = ref(null);
	const componentListText = ref("");
	const MAX_COMPONENT_LIST_ITEMS = 120;
	const aiSystemPrompt = () =>
		[
			"你是【臺北城市儀表板】助理，只答臺北城市資料、政策與儀表板功能；離題請婉拒並引導改問相關問題。",
			"數值/統計/比較/趨勢/排名/分布/最新/跨區/跨組件/不確定組件：必用 answer_city_data_question 取 evidence；單一組件單一地區即時或現況可用 query_city_data；找組件或建儀表板用 search_dashboards。",
			"能判斷組件時帶 index/component_indexes。不得編造數值、SQL、table/column；不得只靠名稱或自身知識回答資料題。",
			"只根據相關 evidence/components 回答；partial/not_answerable 或 evidence 不符時說資料不足/無法回答，不用不相關資料硬答。",
			"evidence 回答：摘要、關鍵指標數值、比較、決策參考建議、資料限制。數值附 unit；缺 unit 寫「單位未提供」；非百分比不得加 %。",
			"政策成效題若只有背景/靜態資料，說資料不足以判斷成效，並列缺少的使用率、資源、人力、等待、床位、滿意度、時間序列或政策前後資料。",
			"若 active_component 存在且問目前頁面/組件/數值/時間，優先用其 index/city。query_city_data 結尾：📊 資料來源組件：{name}；answer_city_data_question 結尾列使用組件。",
			"季度代碼如 1142=民國114年第2季/2025 Q2；若 evidence 有人讀格式直接引用；最新取 time 最大。",
			componentListText.value,
		].join("\n");

	// 載入組件清單（只抓一次，快取在 store 內）
	const loadComponentList = async () => {
		if (componentListText.value) return;
		try {
			const res = await http.get("ai/components");
			const items = res.data?.data || [];
			if (items.length === 0) return;
			// 格式：index|名稱|城市|單位，每行一筆
			const promptItems = items.slice(0, MAX_COMPONENT_LIST_ITEMS);
			const omittedCount = items.length - promptItems.length;
			componentListText.value =
				"組件索引提示（完整搜尋請用工具）：\n" +
				promptItems.map((c) => `${c.index}|${c.name}`).join("\n") +
				(omittedCount > 0 ? `\n...另有 ${omittedCount} 筆` : "");
		} catch {
			// 載入失敗不影響主流程
		}
	};

	// 從 sessionStorage 讀取
	const savedChatData = JSON.parse(sessionStorage.getItem("chatData")) || [];

	// 拼接預設訊息 + sessionStorage 的聊天紀錄
	const chatData = ref([...defaultChatData, ...savedChatData]);

	// 監聽 chatData 的變化，自動同步到 sessionStorage
	watch(
		chatData,
		(newVal) => {
			// 只存使用者與機器人的聊天訊息，不存重複的預設訊息
			const userBotMessages = newVal.filter((item) => !item.isDefault);
			sessionStorage.setItem("chatData", JSON.stringify(userBotMessages));
		},
		{ deep: true },
	);

	const addChatData = (newChatData) => {
		chatData.value.push({
			id: chatData.value.length + 1,
			isDefault: false,
			...newChatData,
		});
	};

	const sendChatMessage = async (userText) => {
		// 第一次發訊息時載入組件清單（後續已快取，立即返回）
		await loadComponentList();

		addChatData({ role: "user", content: userText });

		const botMsgId = chatData.value.length + 1;
		chatData.value.push({
			id: botMsgId,
			role: "bot",
			isDefault: false,
			content: "大腦運轉中，請稍候...",
			isLoading: true,
		});

		try {
			const response = await http.post("ai/chat/twai", {
				messages: [
					{
						role: "system",
						content: aiSystemPrompt(),
					},
					{ role: "user", content: userText },
				],
				stream: false,
				tools: [
					{
						type: "function",
						function: {
							name: "search_dashboards",
							description:
								"搜尋相關儀表板組件；用於找組件或建立儀表板。",
							parameters: {
								type: "object",
								properties: {
									query: {
										type: "string",
										description: "查詢關鍵字或主題",
									},
								},
								required: ["query"],
							},
						},
					},
					{
						type: "function",
						function: {
							name: "answer_city_data_question",
							description:
								"取得城市資料題的 evidence JSON；用於比較、趨勢、排名、跨區/跨組件、政策成效或不確定組件時。",
							parameters: {
								type: "object",
								properties: {
									user_question: {
										type: "string",
										description:
											"使用者完整問題",
									},
									city: {
										type: "string",
										enum: ["taipei", "metrotaipei"],
										description:
											"城市範圍，預設 taipei",
									},
									component_indexes: {
										type: "array",
										items: { type: "string" },
										description:
											"明確識別的組件 index 陣列",
									},
									time_from: {
										type: "string",
										description:
											"查詢起始時間",
									},
									time_to: {
										type: "string",
										description:
											"查詢結束時間",
									},
									top_k: {
										type: "integer",
										description:
											"最多搜尋組件數，預設 5，最大 8",
										minimum: 1,
										maximum: 8,
									},
									score_threshold: {
										type: "number",
										description:
											"語意搜尋門檻，預設 0.75",
										minimum: 0,
										maximum: 1,
									},
								},
								required: ["user_question"],
							},
						},
					},
					{
						type: "function",
						function: {
							name: "query_city_data",
							description:
								"查單一組件/地區的具體即時或現況數據。",
							parameters: {
								type: "object",
								properties: {
									index: {
										type: "string",
										description:
											"明確識別的組件 index",
									},
									query: {
										type: "string",
										description: "查詢主題或關鍵字",
									},
									city: {
										type: "string",
										description:
											"城市名稱，預設 taipei",
									},
									time_from: {
										type: "string",
										description:
											"查詢起始時間",
									},
									time_to: {
										type: "string",
										description:
											"查詢結束時間",
									},
								},
								required: ["query"],
							},
						},
					},
				],
			});

			const data = response.data?.data;
			const aiAnswer = data?.content || "";
			const componentResultsRaw = data?.component_results;

			const targetMsg = chatData.value.find((msg) => msg.id === botMsgId);
			if (!targetMsg) return;

			// 若 AI 呼叫了 search_dashboards，解析組件結果並顯示表格
			if (componentResultsRaw) {
				try {
					const components = JSON.parse(componentResultsRaw);
					if (components.length > 0) {
						targetMsg.content =
							aiAnswer ||
							"以下是根據您的問題，自動為您推薦的「組件清單」。您可以將這些組件整批加入「個人儀表板」。";
						targetMsg.isLoading = false;

						chatData.value.push({
							id: chatData.value.length + 1,
							role: "bot",
							isDefault: false,
							button: [{ id: 1, text: "建立儀表板" }],
							content: null,
							relations: components,
						});

						saveChatLog(userText, components);
						return;
					}
				} catch {
					// JSON 解析失敗，繼續顯示文字回答
				}
			}

			// 一般文字回答
			targetMsg.content = aiAnswer || "抱歉，我沒有得出結論。";
			targetMsg.isLoading = false;
		} catch (error) {
			console.error("AI Chat Error:", error);
			const targetMsg = chatData.value.find((msg) => msg.id === botMsgId);
			if (targetMsg) {
				targetMsg.content = "抱歉，AI 系統目前連線不穩定，請稍後再試！";
				targetMsg.isLoading = false;
			}
		}
	};

	const addQueryData = async (newChatData) => {
		chatData.value.push({
			id: chatData.value.length + 1,
			isDefault: false,
			...newChatData,
		});

		recommendComponents.value = [];
		let topK = null;

		try {
			const response = await http.post(
				"/vector/component",
				new URLSearchParams({
					query: newChatData.content,
					limit: 10,
					score: 0.8,
				}),
				{
					headers: {
						"Content-Type": "application/x-www-form-urlencoded",
					},
				},
			);
			if (response.data?.data?.length > 0) {
				recommendComponents.value = response.data.data;
			}

			// 去除重複項目存到 result
			const result = Array.from(
				recommendComponents.value
					.reduce((map, item) => {
						const key = item.index;
						const exist = map.get(key);

						// 如果還沒放過，直接放
						if (!exist) {
							map.set(key, item);
							return map;
						}

						// 如果已存在，但現在的是 metrotaipei，就覆蓋
						if (item.city === "metrotaipei") {
							map.set(key, item);
						}

						return map;
					}, new Map())
					.values(),
			);
			// 把 result 蓋回去 recommendComponents
			recommendComponents.value = result;
		} catch (error) {
			console.error("VectorAnalysisError :", error);
		}

		if (
			recommendComponents.value &&
			recommendComponents.value?.length > 0
		) {
			topK = [...recommendComponents.value].sort(
				(a, b) => b.score - a.score,
			);
			chatData.value.push({
				id: chatData.value.length + 1,
				role: "bot",
				isDefault: false,
				button: [{ id: 1, text: "建立儀表板" }],
				content: `您好 😊 \n 以下是根據您的問題，自動為您推薦的「組件清單」。您可以將這些組件整批加入「個人儀表板」，方便日後快速查看與使用。\n`,
				relations: topK,
			});
			chatData.value.push({
				id: chatData.value.length + 1,
				role: "bot",
				isDefault: false,
				content: `若您有任何新的查詢或想深入探索的內容，都可以隨時在對話框告訴我～\n 我很樂意再協助您 💬✨`,
			});
		} else {
			chatData.value.push({
				id: chatData.value.length + 1,
				role: "bot",
				isDefault: false,
				content: `很抱歉，您提供的描述沒有相似組件，請繼續提問 ! `,
			});
		}

		// 分析結束後紀錄問答log
		saveChatLog(newChatData.content, recommendComponents.value);
	};

	const saveChatLog = async (question, answer) => {
		try {
			const formData = new FormData();
			const d = new Date();
			const todayId =
				d.getFullYear() +
				String(d.getMonth() + 1).padStart(2, "0") +
				String(d.getDate()).padStart(2, "0");

			formData.append("session", "session_" + todayId);
			formData.append("question", question);
			formData.append("answer", JSON.stringify(answer));

			await http.post("/chatlog/", formData, {
				headers: {
					"Content-Type": "multipart/form-data",
				},
			});
		} catch (error) {
			console.error("saveChatLog error:", error);
		}
	};

	return {
		chatData,
		addChatData,
		addQueryData,
		saveChatLog,
		sendChatMessage,
	};
});
