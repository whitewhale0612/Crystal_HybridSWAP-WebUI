/* pages/overview.js — live status dashboard. */

import { html } from "../../vendor/preact.mjs";
import { useStore, refreshStatus } from "../store.js";
import { Card, Stat, Ring, Meter, Banner, KV, Chip, Button, Skeleton, IOSummary } from "../components/ui.js";
import { ModeChip, OnOffChip } from "../components/badges.js";
import { fmtBytes, fmtPct, fmtInt, pct, fmtQuota } from "../format.js";

function LoadingCards() {
  return html`<div class="grid">
    ${[0, 1, 2, 3].map(
      () => html`<${Card}><div class="stack stack--sm">
        <${Skeleton} w="40%" h=${12} />
        <${Skeleton} w="70%" h=${26} />
        <${Skeleton} w="100%" h=${8} />
      </div><//>`
    )}
  </div>`;
}

export function Overview() {
  const { status, loading } = useStore((s) => ({ status: s.status, loading: s.statusLoading }));

  if (!status) return LoadingCards();

  const z = status.zram || {};
  const sw = status.swap || {};
  const zms = status.zms || {};
  const c = status.crystal || {};
  const bd = c.bd_stat_io || {};
  const zmsPhysical = zms.physical_io || {};
  const zmsPhysicalFailed = (zmsPhysical.write_failed_pages || 0) + (zmsPhysical.read_failed_pages || 0);
  const mm = z.mm_stat || {};

  const swapRatio = pct(sw.used_bytes, sw.size_bytes);
  const zramCompressionRatio = pct(mm.compr_data_size, mm.orig_data_size);
  const quotaRatio = pct(c.quota_used, c.quota_day);
  const blockRatio = pct(zms.used_blocks, zms.nr_blocks);
  const blockUtil = zms.block_utilization_pct ?? zms.packing_efficiency_pct;
  const backingSaved = zms.backing_savings_pct ?? zms.space_saved_pct;
  const originalBytes =
    zms.original_bytes ?? (zms.objects != null ? Number(zms.objects) * 4096 : null);
  const slotOverhead = zms.slot_overhead_bytes;
  const slotOverheadPct = zms.slot_overhead_pct;
  const signedBytes = (v) => (v > 0 ? `+${fmtBytes(v)}` : fmtBytes(v));
  const bdMeta = bd.present
    ? html`<span>当前已回写 ${fmtBytes(bd.current_bytes)}</span><br />
        <span>仅统计实际换出/读回，不含存储整理</span>`
    : "暂无回写路径统计";
  const zmsPhysicalMeta = zmsPhysical.present
    ? html`<span>写 I/O ${fmtInt(zmsPhysical.write_ios)} 次 · 读 ${fmtInt(zmsPhysical.read_ios)} 次 ·
        ${zmsPhysicalFailed > 0
          ? html`<span style="color:var(--danger)">失败 ${fmtInt(zmsPhysicalFailed)} 页</span>`
          : "失败 0 页"}</span><br />
        <span>包含存储整理产生的额外读写</span>`
    : "暂无全量物理 I/O 统计";
  // ZMS stats are only meaningful when the subsystem is enabled and its stat
  // node was read. Otherwise the zeros are "no data", not a real 0.
  const zmsLive = zms.enabled === 1 && zms.present !== false;

  return html`<div class="stack fade-in">
    <!-- hero -->
    <${Card} accent=${true}>
      <div class="row row--between row--wrap" style="gap:12px">
        <div class="stack stack--sm">
          <div class="pill-row">
            ${ModeChip({ mode: status.mode })}
            ${OnOffChip({ on: sw.active, label: "Swap" })}
            ${Chip({ kind: z.initstate === 1 ? "ok" : "muted", dot: true, children: z.initstate === 1 ? "ZRAM 已初始化" : "ZRAM 未初始化" })}
          </div>
          <div class="muted" style="font-size:var(--fs-sm)">
            设备 <b class="mono">${z.device || "—"}</b> · 压缩 <b>${z.comp_algorithm || "—"}</b>
            ${z.backing_bound_via && z.backing_bound_via !== "none"
              ? html` · 后端 <b>${z.backing_bound_via}</b>`
              : ""}
          </div>
        </div>
        <${Button} size="sm" variant="ghost" icon="refresh" loading=${loading} onClick=${refreshStatus}>刷新<//>
      </div>

      ${status.degraded &&
      html`<div class="stack stack--sm" style="margin-top:14px">
        ${(status.degraded_reasons && status.degraded_reasons.length
          ? status.degraded_reasons
          : ["后端报告了降级状态"]
        ).map((r) => Banner({ kind: "degraded", title: "降级", children: r }))}
      </div>`}
    <//>

    <div class="grid grid--2">
      <!-- swap ring -->
      <${Card} title="Swap 使用" icon="database">
        <div class="row" style="gap:18px;align-items:center">
          ${Ring({
            value: swapRatio,
            label: fmtPct(swapRatio),
            sublabel: sw.active ? "已激活" : "未激活",
            tone: swapRatio > 90 ? "danger" : swapRatio > 75 ? "warn" : "accent",
          })}
          <div class="stack stack--sm" style="flex:1;min-width:0">
            ${Stat({
              label: "已用 / 总量",
              value: fmtBytes(sw.used_bytes),
              sub: `共 ${fmtBytes(sw.size_bytes)}`,
            })}
            ${KV({ k: "压缩后大小", children: fmtBytes(mm.compr_data_size) })}
            ${KV({ k: "压缩率", children: fmtPct(zramCompressionRatio, 1) })}
            ${KV({ k: "设备", mono: true, children: sw.device || "—" })}
          </div>
        </div>
      <//>

      <!-- zms efficiency -->
      <${Card} title="ZMS 存储效率" icon="layers">
        ${!zmsLive
          ? html`<div class="stack stack--sm">
              <div class="pill-row">
                ${Chip({ kind: "muted", dot: true, children: zms.enabled === 1 ? "ZMS 已启用" : "ZMS 未启用" })}
              </div>
              <div class="muted" style="font-size:var(--fs-sm)">
                当前无 ZMS 统计数据${zms.enabled === 1 ? "（尚未产生块使用）" : ""}。
              </div>
            </div>`
          : html`<div>
	              <div class="row" style="gap:18px;align-items:flex-start">
	                <div class="stack stack--sm" style="flex:1">
	                  ${Stat({
	                    label: "节省率",
	                    value: backingSaved == null ? "—" : `${Number(backingSaved).toFixed(2)}`,
	                    unit: "%",
	                    sub: `块利用率 ${fmtPct(blockUtil, 1)}`,
	                  })}
	                  ${KV({ k: "原始大小", mono: true, children: fmtBytes(originalBytes) })}
	                  ${KV({ k: "真实 / 槽位", mono: true, children: `${fmtBytes(zms.stored_bytes)} · ${fmtBytes(zms.packed_bytes)}` })}
	                  ${KV({ k: "槽位开销", mono: true, children: `${signedBytes(slotOverhead)} · ${fmtPct(slotOverheadPct, 2)}` })}
	                </div>
	              </div>
              <div style="margin-top:12px">
                ${Meter({
                  label: `块使用 ${fmtInt(zms.used_blocks)} / ${fmtInt(zms.nr_blocks)}`,
                  valueText: fmtPct(blockRatio, 1),
                  ratio: blockRatio,
                  tone: blockRatio > 90 ? "warn" : "accent",
                })}
              </div>
              <div class="divider"></div>
              ${IOSummary({
                writeLabel: "全量物理写入",
                readLabel: "全量物理读回",
                writeBytes: zmsPhysical.write_bytes,
                writeCount: zmsPhysical.write_pages,
                readBytes: zmsPhysical.read_bytes,
                readCount: zmsPhysical.read_pages,
                countUnit: "页",
                meta: zmsPhysicalMeta,
              })}
            </div>`}
      <//>

      <!-- crystal quota -->
      <${Card} title="回写配额" icon="snow">
        <div class="stack stack--sm">
          <div class="pill-row">
            ${OnOffChip({ on: c.enable === 1, label: "Hybridswap" })}
            ${OnOffChip({ on: c.core_enable === 1, label: "Core" })}
            ${Chip({ kind: c.swapd_pause === 1 ? "warn" : "muted", children: `swapd ${c.swapd_pause === 1 ? "暂停" : "运行"}` })}
          </div>
          ${Meter({
            label: `日配额已用 ${fmtBytes(c.quota_used)}`,
            valueText: fmtPct(quotaRatio, 0),
            ratio: quotaRatio,
            tone: quotaRatio > 90 ? "danger" : quotaRatio > 75 ? "warn" : "accent",
          })}
          ${KV({ k: "配额上限", children: fmtQuota(c.quota_day) })}
          ${KV({ k: "剩余", children: fmtBytes(c.quota_remaining) })}
          ${KV({ k: "ZRAM 压力增量", children: c.zram_increase == null ? "—" : `${c.zram_increase} MiB` })}
          <div class="divider"></div>
          ${IOSummary({
            writeLabel: "数据路径写入",
            readLabel: "数据路径读回",
            writeBytes: bd.write_bytes,
            writeCount: bd.write_blocks,
            readBytes: bd.read_bytes,
            readCount: bd.read_blocks,
            countUnit: "4K 块",
            meta: bdMeta,
          })}
        </div>
      <//>

      <!-- zram detail -->
      <${Card} title="ZRAM 概况" icon="chip">
        <div>
          ${KV({ k: "设备", mono: true, children: z.device || "—" })}
          ${KV({ k: "初始化状态", children: z.initstate === 1 ? "已初始化" : "未初始化" })}
          ${KV({ k: "磁盘大小", children: fmtBytes(z.disksize_bytes) })}
          ${KV({ k: "内存上限", children: z.mem_limit_bytes ? fmtBytes(z.mem_limit_bytes) : "不限制" })}
          ${KV({ k: "压缩算法", children: z.comp_algorithm || "—" })}
          ${KV({ k: "后端绑定", children: z.backing_bound_via || "none" })}
          ${z.backing_dev && KV({ k: "后端设备", mono: true, children: z.backing_dev })}
        </div>
      <//>
    </div>
  </div>`;
}
