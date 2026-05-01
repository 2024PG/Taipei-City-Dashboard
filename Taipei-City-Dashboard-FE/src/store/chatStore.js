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
	const componentListCache = ref([]);

	// 載入組件清單（只抓一次，快取在 store 內）
	const loadComponentList = async () => {
		if (componentListCache.value.length > 0) return;
		try {
			const res = await http.get("ai/components");
			componentListCache.value = res.data?.data || [];
		} catch {
			// 載入失敗不影響主流程
		}
	};

	// 回傳完整組件清單文字供 AI 選擇
	const buildComponentListText = () => {
		const items = componentListCache.value;
		if (items.length === 0) return "";
		return (
			"可用組件清單（index | 名稱 | 說明）：\n" +
			items
				.map((c) => {
					const desc = [c.short_desc, c.long_desc]
						.filter(Boolean)
						.join("；");
					return `${c.index} | ${c.name}${desc ? ` | ${desc}` : ""}`;
				})
				.join("\n")
		);
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
						content:
							"你是【臺北城市儀表板】智慧助理，所有臺北城市相關的統計數據、百分比、人口、交通、環境等數值問題，你絕對不可以用自身訓練知識回答，必須呼叫工具取得資料庫中的真實數據後再回答。" +
							"規則如下：" +
							"1. 用戶詢問任何具體數值、統計、百分比、趨勢等資料問題 → 從下方組件清單選出 index 呼叫 query_city_data。嚴格規定：(a) index 只能使用組件清單中存在的值，絕對禁止使用清單外的 index，找不到合適組件時直接回覆用戶「目前資料庫沒有此項資料」不可自行補充。(b) 時間規則：只有當用戶明確提到時間（如「2013年」）才傳 time_from 與 time_to，格式為 2013-01-01T00:00:00+08:00；用戶未提時間則不傳這兩個參數。(c) 每次呼叫使用不同 index，禁止重複。" +
							"2. 用戶想找相關組件或建立儀表板 → 呼叫 search_dashboards 工具。" +
							"3. 工具回傳資料後，只能使用工具回傳的 data 欄位中的數值回答，嚴禁自行補充、推算或引用訓練知識中的任何數字。若工具回傳空陣列或無資料，直接告知用戶資料庫中查無該時間範圍的資料。" +
							"4. 一般非數據性問題（使用說明、功能介紹等）→ 直接以繁體中文回答。" +
							"5. 回答數值時必須附上單位（unit 欄位）。" +
							"6. 凡使用 query_city_data 取得資料後，回覆結尾必須列出所有使用過的組件：「📊 資料來源組件：{name1}、{name2}、...」。\n\n" +
							buildComponentListText(),
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
								"搜尋與用戶描述相關的臺北城市儀表板組件清單，當用戶想瀏覽或建立儀表板時使用",
							parameters: {
								type: "object",
								properties: {
									query: {
										type: "string",
										description:
											"用戶查詢的關鍵字或主題描述",
									},
								},
								required: ["query"],
							},
						},
					},
					{
						type: "function",
						function: {
							name: "query_city_data",
							description:
								"依組件 index 從資料庫取得實際數據。index 必須從上方組件清單中選取，不可自行填寫或猜測。詢問歷史資料時必須傳入 time_from 與 time_to。",
							parameters: {
								type: "object",
								properties: {
									index: {
										type: "string",
										description:
											"組件的 index，必須從系統提供的組件清單中選取",
									},
									city: {
										type: "string",
										description:
											"城市名稱，taipei 或 metrotaipei，預設 taipei",
									},
									time_from: {
										type: "string",
										description:
											"查詢起始時間，格式 2006-01-02T15:04:05+08:00，不填則預設最近 24 小時",
									},
									time_to: {
										type: "string",
										description:
											"查詢結束時間，格式 2006-01-02T15:04:05+08:00，不填則為現在",
									},
								},
								required: ["index"],
							},
						},
					},
				],
			});

			const data = response.data?.data;
			const aiAnswer = data?.content || "";
			const componentResultsRaw = data?.component_results;
			// const toolCallsLogRaw = data?.tool_calls_log;

			// 顯示工具呼叫 log（除錯用，需要時取消註解）
			// if (toolCallsLogRaw) {
			// 	try {
			// 		const calls = JSON.parse(toolCallsLogRaw);
			// 		const logLines = calls.map((c) => {
			// 			const args = JSON.parse(c.args || "{}");
			// 			const detail = Object.entries(args)
			// 				.map(([k, v]) => `${k}=${v}`)
			// 				.join(", ");
			// 			return `🔧 ${c.tool}(${detail})`;
			// 		});
			// 		chatData.value.push({
			// 			id: chatData.value.length + 1,
			// 			role: "bot",
			// 			isDefault: false,
			// 			isDebug: true,
			// 			content: logLines.join("\n"),
			// 		});
			// 	} catch {
			// 		// 解析失敗不影響主流程
			// 	}
			// }

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
