<script setup>
import { ref, computed } from "vue";
import TimelineSeparateChart from "./TimelineSeparateChart.vue";

const props = defineProps([
	"chart_config",
	"activeChart",
	"series",
]);

const labels = computed(
	() => props.chart_config?.categories ?? ["第一層", "第二層", "第三層"]
);

const normalizedSeries = computed(() =>
	(props.series || []).map((row) => ({
		filterLevel1: row.filter_level_1 ?? "",
		filterLevel2: row.filter_level_2 ?? "",
		filterLevel3: row.filter_level_3 ?? "",
		time: row.time ?? "",
		seriesName: row.series_name ?? "數值",
		value: Number(row.value ?? row.data ?? row.y ?? 0),
	}))
);

const selectedLevel1 = ref("");
const selectedLevel2 = ref("");
const selectedLevel3 = ref("");

const level1Options = computed(() => [
	...new Set(
		normalizedSeries.value
			.map((row) => row.filterLevel1)
			.filter(Boolean)
	),
]);

const level2Options = computed(() => {
	const base = normalizedSeries.value.filter((row) => {
		if (selectedLevel1.value && row.filterLevel1 !== selectedLevel1.value) {
			return false;
		}
		return true;
	});

	return [
		...new Set(
			base
				.map((row) => row.filterLevel2)
				.filter(Boolean)
		),
	];
});

const level3Options = computed(() => {
	const base = normalizedSeries.value.filter((row) => {
		if (selectedLevel1.value && row.filterLevel1 !== selectedLevel1.value) {
			return false;
		}
		if (selectedLevel2.value && row.filterLevel2 !== selectedLevel2.value) {
			return false;
		}
		return true;
	});

	return [
		...new Set(
			base
				.map((row) => row.filterLevel3)
				.filter(Boolean)
		),
	];
});

const selectedLevel1Model = computed({
	get: () => selectedLevel1.value,
	set: (value) => {
		selectedLevel1.value = value;
		selectedLevel2.value = "";
		selectedLevel3.value = "";
	},
});

const selectedLevel2Model = computed({
	get: () => selectedLevel2.value,
	set: (value) => {
		selectedLevel2.value = value;
		selectedLevel3.value = "";
	},
});

const filteredRows = computed(() =>
	normalizedSeries.value.filter((row) => {
		if (selectedLevel1.value && row.filterLevel1 !== selectedLevel1.value) {
			return false;
		}
		if (selectedLevel2.value && row.filterLevel2 !== selectedLevel2.value) {
			return false;
		}
		if (selectedLevel3.value && row.filterLevel3 !== selectedLevel3.value) {
			return false;
		}
		return true;
	})
);

const timelineSeries = computed(() => {
	const grouped = {};

	filteredRows.value.forEach((row) => {
		if (!row.time) return;

		const seriesKey = row.seriesName;
		const timeKey = String(row.time);

		if (!grouped[seriesKey]) {
			grouped[seriesKey] = {};
		}

		grouped[seriesKey][timeKey] =
			(grouped[seriesKey][timeKey] || 0) + row.value;
	});

	return Object.entries(grouped).map(([seriesName, valuesByTime]) => ({
		name: seriesName,
		data: Object.entries(valuesByTime)
			.map(([x, y]) => ({ x, y }))
			.sort((a, b) => new Date(a.x).getTime() - new Date(b.x).getTime()),
	}));
});

const timelineChartConfig = computed(() => ({
	...props.chart_config,
	unit: props.chart_config?.unit ?? "",
	color: props.chart_config?.color ?? [],
}));
</script>

<template>
  <div
    v-if="activeChart === 'CascadeTimelineChart'"
    class="cascade-timeline-chart"
  >
    <div class="cascade-timeline-filters">
      <label class="cascade-timeline-filter">
        <span>{{ labels[0] }}</span>
        <select v-model="selectedLevel1Model">
          <option value="">全部</option>
          <option
            v-for="option in level1Options"
            :key="option"
            :value="option"
          >
            {{ option }}
          </option>
        </select>
      </label>

      <label class="cascade-timeline-filter">
        <span>{{ labels[1] }}</span>
        <select
          v-model="selectedLevel2Model"
          :disabled="level2Options.length === 0"
        >
          <option value="">全部</option>
          <option
            v-for="option in level2Options"
            :key="option"
            :value="option"
          >
            {{ option }}
          </option>
        </select>
      </label>

      <label class="cascade-timeline-filter">
        <span>{{ labels[2] }}</span>
        <select
          v-model="selectedLevel3"
          :disabled="level3Options.length === 0"
        >
          <option value="">全部</option>
          <option
            v-for="option in level3Options"
            :key="option"
            :value="option"
          >
            {{ option }}
          </option>
        </select>
      </label>
    </div>

    <TimelineSeparateChart
      v-if="timelineSeries.length > 0 && timelineSeries[0].data.length > 0"
      active-chart="TimelineSeparateChart"
      :chart_config="timelineChartConfig"
      :series="timelineSeries"
    />

    <p
      v-else
      class="cascade-timeline-empty"
    >
      無符合條件的資料
    </p>
  </div>
</template>

<style scoped>
.cascade-timeline-chart {
  padding: 0 0.5rem;
  overflow-x: auto;
}

.cascade-timeline-filters {
  display: flex;
  gap: 8px;
  margin-bottom: 8px;
  flex-wrap: wrap;
}

.cascade-timeline-filter {
  display: flex;
  flex-direction: column;
  gap: 4px;
  flex: 1;
  min-width: 100px;
}

.cascade-timeline-filter span {
  font-size: 0.7rem;
  color: var(--color-complement-text);
  letter-spacing: 0.5px;
}

.cascade-timeline-filter select {
  padding: 4px 8px;
  background: var(--color-component-background);
  color: var(--color-normal-text);
  border: 1px solid var(--color-border);
  border-radius: 4px;
  font-size: 0.8rem;
  cursor: pointer;
  width: 100%;
}

.cascade-timeline-filter select:disabled {
  opacity: 0.35;
  cursor: not-allowed;
}

.cascade-timeline-empty {
  text-align: center;
  color: var(--color-complement-text);
  padding: 24px 0;
  font-size: 0.85rem;
}
</style>
