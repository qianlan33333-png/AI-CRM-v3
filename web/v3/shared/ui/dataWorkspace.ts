import { TabulatorFull, type ColumnDefinition } from "tabulator-tables";
import { init, use, type EChartsType } from "echarts/core";
import { BarChart } from "echarts/charts";
import {
  GridComponent,
  TooltipComponent,
  TitleComponent,
} from "echarts/components";
import { CanvasRenderer } from "echarts/renderers";
use([
  BarChart,
  GridComponent,
  TooltipComponent,
  TitleComponent,
  CanvasRenderer,
]);

export interface WorkspaceMetric {
  key: string;
  label: string;
  value: number | null;
  percentage?: number | null;
  distribution?: { label: string; value: number }[];
}
export interface WorkspacePresentation {
  columns?: string[];
  widths?: Record<string, number>;
  metrics?: string[];
  order?: string[];
}
// Querying and authorization belong to the caller. The grid never filters a
// page locally or derives total/group counts from its loaded rows.
export class DataWorkspace {
  private grid: TabulatorFull;
  private chart: EChartsType;
  private tierChart: EChartsType;
  private tierPlot = document.createElement("div");
  private cards = document.createElement("div");
  private settings = document.createElement("details");
  private plot = document.createElement("div");
  private observer: ResizeObserver;
  private ready: Promise<void>;
  private metrics: WorkspaceMetric[] = [];
  private metricOptions = document.createElement("div");
  private changed?: (value: WorkspacePresentation) => void;
  private presentation: WorkspacePresentation = {};
  private defaultColumns: string[] = [];
  constructor(
    readonly root: HTMLElement,
    columns: ColumnDefinition[],
    onChange?: (value: WorkspacePresentation) => void,
  ) {
    if (!document.getElementById("data-workspace-style")) {
      const style = document.createElement("style");
      style.id = "data-workspace-style";
      style.textContent = `.dw-page button,.dw-page select,.dw-page input:not([type=checkbox]),.dw-dialog button,.dw-dialog select,.dw-dialog input:not([type=checkbox]){font:inherit;border:1px solid #dee0e3;border-radius:6px;background:white;padding:7px 12px;color:#1f2329;min-height:34px;box-sizing:border-box}.dw-page button,.dw-dialog button{cursor:pointer}.dw-page button:hover,.dw-dialog button:hover{border-color:#3370ff;color:#245bdb}.dw-page button:disabled{opacity:.45;cursor:default}.dw-page input:focus,.dw-page select:focus{outline:2px solid #c2d4ff}.dw-page [role=status]{font-size:13px;color:#646a73;padding:8px 0}.dw-query{border:1px solid #e5e6eb;border-radius:8px;padding:12px;background:#fff;margin:12px 0}.dw-orders{display:flex;gap:16px;flex-wrap:wrap}.dw-dialog::backdrop{background:#1f232966}.data-workspace{font:14px system-ui;color:#1f2329;min-width:0}.dw-cards{display:flex;flex-wrap:wrap;gap:12px;margin:16px 0}.dw-card{flex:1;min-width:145px;border:1px solid #e5e6eb;border-radius:8px;padding:16px;background:white}.dw-card strong{display:block;font-size:26px;margin-top:8px}.dw-plot{height:220px}.data-workspace details{padding:8px 0}.data-workspace label{display:inline-flex;gap:6px;margin:6px 14px 6px 0}.data-workspace .tabulator{position:relative;border:1px solid #e5e6eb;background:white;text-align:left;overflow:hidden}.data-workspace .tabulator-header{position:relative;overflow:hidden;background:#f5f6f7;font-weight:600;white-space:nowrap}.data-workspace .tabulator-header-contents,.data-workspace .tabulator-headers{position:relative;display:inline-block}.data-workspace .tabulator-col{display:inline-flex;position:relative;flex-direction:column;box-sizing:border-box;border-right:1px solid #e5e6eb}.data-workspace .tabulator-col-content{padding:12px;position:relative}.data-workspace .tabulator-col-title{overflow:hidden;text-overflow:ellipsis}.data-workspace .tabulator-col-resize-handle{position:absolute;right:0;top:0;width:6px;height:100%;cursor:col-resize}.data-workspace .tabulator-tableholder{position:relative;overflow:auto;white-space:nowrap}.data-workspace .tabulator-table{position:relative;display:inline-block}.data-workspace .tabulator-row{position:relative;box-sizing:border-box;white-space:nowrap;border-bottom:1px solid #eff0f1}.data-workspace .tabulator-row:hover{background:#f5f8ff}.data-workspace .tabulator-cell{display:inline-block;position:relative;box-sizing:border-box;padding:12px;overflow:hidden;text-overflow:ellipsis;vertical-align:middle;border-right:1px solid #eff0f1}.data-workspace .tabulator-group{padding:12px;cursor:pointer;background:#f5f6f7}.data-workspace .tabulator-arrow{display:inline-block;margin-right:8px;border-left:6px solid #646a73;border-top:4px solid transparent;border-bottom:4px solid transparent}.data-workspace .tabulator-group-visible .tabulator-arrow{transform:rotate(90deg)}.data-workspace .tabulator-placeholder{padding:30px;text-align:center}.data-workspace .tabulator-footer{display:none}@media(max-width:600px){.dw-card{min-width:100%;box-sizing:border-box}}`;
      document.head.append(style);
    }
    this.changed = onChange;
    this.defaultColumns = columns
      .filter((c) => c.visible !== false)
      .map((c) => c.field!)
      .filter(Boolean);
    this.settings.append(this.metricOptions);
    root.classList.add("data-workspace");
    this.cards.className = "dw-cards";
    this.plot.className = "dw-plot";
    this.tierPlot.className = "dw-plot";
    this.tierPlot.hidden = true;
    const summary = document.createElement("summary");
    summary.textContent = "显示列与指标";
    this.settings.prepend(summary);
    const table = document.createElement("div");
    root.append(this.settings, this.cards, this.plot, this.tierPlot, table);
    this.grid = new TabulatorFull(table, {
      height: 480,
      layout: "fitData",
      placeholder: "没有符合条件的数据",
      columns: columns.map((c) => ({
        ...c,
        headerSort: false,
        formatter: c.formatter || "plaintext",
      })),
      data: [],
      movableColumns: true,
      groupToggleElement: "header",
    });
    this.ready = new Promise((resolve) => this.grid.on("tableBuilt", resolve));
    this.chart = init(this.plot);
    this.tierPlot.hidden = false;
    this.tierChart = init(this.tierPlot);
    this.tierPlot.hidden = true;
    this.observer = new ResizeObserver(() => {
      this.chart.resize();
      if (!this.tierPlot.hidden) this.tierChart.resize();
    });
    this.observer.observe(this.plot);
    const changed = () => {
      this.presentation.widths = Object.fromEntries(
        this.grid.getColumns().map((c) => [c.getField(), c.getWidth()]),
      );
      onChange?.(structuredClone(this.presentation));
    };
    this.grid.on("columnResized", changed);
    this.grid.on("columnMoved", () => {
      this.presentation.order = this.grid.getColumns().map((c) => c.getField());
      changed();
    });
    this.ready.then(() => {
      for (const c of columns) {
        if (!c.field) continue;
        const label = document.createElement("label");
        const input = document.createElement("input");
        input.type = "checkbox";
        input.checked = c.visible !== false;
        input.dataset.column = c.field;
        label.append(input, document.createTextNode(String(c.title)));
        input.onchange = () => {
          if (input.checked) this.grid.showColumn(c.field!);
          else this.grid.hideColumn(c.field!);
          this.presentation.columns = this.grid
            .getColumns()
            .filter((c) => c.isVisible())
            .map((c) => c.getField());
          changed();
        };
        this.settings.append(label);
      }
    });
  }
  placeToolbar(toolbar: HTMLElement) {
    this.tierPlot.after(toolbar);
  }
  async render(
    rows: Record<string, unknown>[],
    metrics: WorkspaceMetric[],
    groupFields: string[] = [],
  ) {
    await this.ready;
    this.metrics = metrics;
    this.metricOptions.replaceChildren();
    for (const metric of metrics) {
      const label = document.createElement("label");
      const input = document.createElement("input");
      input.type = "checkbox";
      input.checked =
        !this.presentation.metrics ||
        this.presentation.metrics.includes(metric.key);
      label.append(input, document.createTextNode(metric.label));
      input.onchange = () => {
        const selected = this.presentation.metrics || metrics.map((m) => m.key);
        this.presentation.metrics = input.checked
          ? [...selected, metric.key]
          : selected.filter((key) => key !== metric.key);
        this.renderMetrics();
        this.changed?.(structuredClone(this.presentation));
      };
      const earlier = document.createElement("button");
      earlier.type = "button";
      earlier.textContent = "↑";
      earlier.setAttribute("aria-label", metric.label + "前移");
      earlier.onclick = () => {
        const order = [
          ...(this.presentation.metrics || metrics.map((m) => m.key)),
        ];
        const index = order.indexOf(metric.key);
        if (index > 0) {
          [order[index - 1], order[index]] = [order[index], order[index - 1]];
          this.presentation.metrics = order;
          this.renderMetrics();
          this.changed?.(structuredClone(this.presentation));
        }
      };
      label.append(earlier);
      this.metricOptions.append(label);
    }

    const keyed = rows.map((row) => {
      const result = { ...row };
      const values = row.__groupValues as Record<string, unknown> | undefined;
      for (const field of groupFields)
        result["__groupKey_" + field] = JSON.stringify(
          values && field in values ? values[field] : row[field],
        );
      return result;
    });
    this.grid.setGroupBy(groupFields.map((field) => "__groupKey_" + field));
    this.grid.setGroupHeader((value, _count, data, group) => {
      const level = group.getField().replace(/^__groupKey_/, "");
      const row = data[0] as Record<string, unknown> | undefined;
      const counts = row?.__groupCounts as Record<string, number> | undefined;
      const span = document.createElement("span");
      span.textContent = `${row?.[level] || "未填写"} · ${counts?.[level] ?? "未知"} 人`;
      return span.outerHTML;
    });
    await this.grid.replaceData(keyed);
    this.renderMetrics();
  }
  private renderMetrics() {
    this.cards.replaceChildren();
    const selected =
      this.presentation.metrics || this.metrics.map((m) => m.key);
    for (const key of selected) {
      const metric = this.metrics.find((m) => m.key === key);
      if (!metric || metric.distribution) continue;
      const card = document.createElement("div");
      card.className = "dw-card";
      const title = document.createElement("span");
      title.textContent = metric.label;
      const value = document.createElement("strong");
      value.textContent =
        metric.value === null ? "未知" : metric.value.toLocaleString();
      card.append(title, value);
      if (metric.percentage !== undefined) {
        const note = document.createElement("small");
        note.textContent =
          metric.percentage === null
            ? "占比未知"
            : `占比 ${metric.percentage.toFixed(1)}%`;
        card.append(note);
      }
      this.cards.append(card);
    }
    const series = this.metrics.filter(
      (m) =>
        m.key !== "total" &&
        m.key !== "expiring_7d" &&
        !m.distribution &&
        selected.includes(m.key),
    );
    const distribution = this.metrics.find(
      (m) => m.distribution && selected.includes(m.key),
    );
    this.tierPlot.hidden = !distribution;
    if (distribution) {
      this.tierChart.resize();
      this.tierChart.setOption(
        {
          animation: false,
          title: { text: distribution.label },
          tooltip: { trigger: "axis", renderMode: "richText" },
          grid: { left: 60, right: 20, bottom: 55, top: 25 },
          xAxis: {
            type: "category",
            data: distribution.distribution!.map((v) => v.label),
          },
          yAxis: { type: "value", minInterval: 1 },
          series: [
            {
              type: "bar",
              data: distribution.distribution!.map((v) => v.value),
              barMaxWidth: 48,
              itemStyle: { color: "#14a39a" },
            },
          ],
        },
        true,
      );
    }
    this.chart.setOption(
      {
        animation: false,
        tooltip: { trigger: "axis", renderMode: "richText" },
        grid: { left: 60, right: 20, bottom: 55, top: 20 },
        xAxis: {
          type: "category",
          data: series.map((m) => m.label),
          axisLabel: { interval: 0, overflow: "truncate", width: 100 },
        },
        yAxis: { type: "value", minInterval: 1 },
        series: [
          {
            type: "bar",
            data: series.map((m) => m.value),
            itemStyle: { color: "#3370ff" },
            barMaxWidth: 48,
          },
        ],
      },
      true,
    );
  }
  async configure(value: WorkspacePresentation) {
    await this.ready;
    this.presentation = structuredClone(value);
    for (const field of [...(value.order || [])].reverse()) {
      const first = this.grid.getColumns()[0]?.getField();
      if (first && field !== first && this.grid.getColumn(field))
        this.grid.moveColumn(field, first, false);
    }
    for (const column of this.grid.getColumns()) {
      const field = column.getField();
      if ((value.columns || this.defaultColumns).includes(field)) column.show();
      else column.hide();
      if (value.widths?.[field]) column.setWidth(value.widths[field]);
      const input = this.settings.querySelector<HTMLInputElement>(
        `input[data-column="${field}"]`,
      );
      if (input) input.checked = column.isVisible();
    }
    this.renderMetrics();
  }
  showDetails(show: boolean) {
    this.root.querySelector<HTMLElement>(".tabulator")!.hidden = !show;
  }
  destroy() {
    this.observer.disconnect();
    this.chart.dispose();
    this.tierChart.dispose();
    this.grid.destroy();
    this.root.replaceChildren();
  }
}
