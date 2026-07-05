/* pages/zram.js — ZRAM setup: disksize slider, comp dropdown, backing store. */

import { html } from "../../vendor/preact.mjs";
import { useStore, setDraftPath } from "../store.js";
import { Card, SelectField, RangeField, NumberField, Segmented, Field, Banner, KV, ToggleRow } from "../components/ui.js";
import { SaveBar, ApplyResult } from "../components/savebar.js";
import { BACKING_MODES, DEFAULT_LOOP_BYTES, MAX_LOOP_BYTES } from "../profile.js";
import { fmtBytes, parseSize } from "../format.js";

const GiB = 1024 * 1024 * 1024;
const MAX_LOOP_GIB = MAX_LOOP_BYTES / GiB;
const BACKING_BLOCK_DEV = "/dev/block/by-name/hybridswap";

const BACKING_LABEL = { none: "无", block: "默认块设备", loop: "LOOP 设备" };

export function ZramSetup() {
  const { draft, errors, status, caps } = useStore((s) => ({
    draft: s.draft,
    errors: s.draftErrors,
    status: s.status,
    caps: s.caps,
  }));
  if (!draft) return null;

  const z = draft.zram;
  const bk = draft.backing;
  const err = (p) => errors[p];

  const zsys = (status && status.zram) || {};
  const zcaps = (caps && caps.zram) || {};
  const initialized = zsys.initstate === 1;

  // disksize slider bounds: max = ceil(RAM) × 2 GiB, step 1 GiB.
  const memBytes = zcaps.mem_total_bytes || 0;
  const ceilGiB = Math.max(1, Math.ceil(memBytes / GiB));
  const maxGiB = Math.max(2, ceilGiB * 2);
  const diskCustom = z.disksize != null && z.disksize !== "";
  const diskGiB = diskCustom ? Math.max(1, Math.round(parseSize(z.disksize) / GiB)) : ceilGiB;

  // compression algorithm dropdown from kernel-supported list.
  const algos = zcaps.comp_algorithms || [];
  const activeAlgo = zsys.comp_algorithm || zcaps.comp_algorithm_active || "";
  const algoOptions = [
    { value: "", label: activeAlgo ? `跟随系统（当前 ${activeAlgo}）` : "跟随系统" },
    ...algos.map((a) => ({ value: a, label: a })),
  ];

  // loop backing size (GiB), default 1 GiB.
  const loopBytes = bk.loop_size_bytes != null ? bk.loop_size_bytes : DEFAULT_LOOP_BYTES;
  const loopGiB = Math.max(1, Math.round(loopBytes / GiB));

  return html`<div class="stack fade-in">
    <div>
      <div class="page-title">ZRAM 设置</div>
      <div class="page-sub">模块完全接管 ZRAM；留空即"跟随系统默认"，守护进程持续维护并抵御外部改动。</div>
    </div>

    <${SaveBar} />

    ${initialized &&
    Banner({
      kind: "info",
      title: "ZRAM 已初始化",
      children: "更改容量/压缩算法/后端存储需要重建 ZRAM，守护进程会在安全时机 swapoff→reset 后重新应用。",
    })}

    <${Card} title="设备与容量" icon="chip">
      <div class="stack">
        ${KV({ k: "目标设备", mono: true, children: z.device || "zram0" })}
        ${ToggleRow({
          label: "自定义磁盘大小",
          desc: diskCustom
            ? "由模块固定为下方容量"
            : `跟随系统默认（内存向上取整 ≈ ${ceilGiB} GiB，当前 ${fmtBytes(zsys.disksize_bytes)}）`,
          checked: diskCustom,
          onChange: (on) => setDraftPath("zram.disksize", on ? `${ceilGiB}G` : null),
        })}
        ${diskCustom &&
        RangeField({
          label: "磁盘大小 (disksize)",
          min: 1,
          max: maxGiB,
          step: 1,
          value: diskGiB,
          valueText: `${diskGiB} GiB`,
          hint: `范围 1 – ${maxGiB} GiB（上限 = 内存 ×2）`,
          error: err("zram.disksize"),
          onChange: (v) => setDraftPath("zram.disksize", `${v}G`),
        })}
        ${SelectField({
          label: "压缩算法 (comp_algorithm)",
          value: z.comp_algorithm ?? "",
          hint: "读取自内核实际支持的算法列表",
          error: err("zram.comp_algorithm"),
          options: algoOptions,
          onChange: (v) => setDraftPath("zram.comp_algorithm", v === "" ? null : v),
        })}
      </div>
    <//>

    <${Card} title="后端存储 (Backing)" icon="layers" note="回写目标：无 / 默认块设备 / LOOP 设备">
      <div class="stack">
        ${Field({
          label: "后端模式",
          error: err("backing.mode"),
          children: Segmented({
            options: BACKING_MODES.map((m) => ({ value: m, label: BACKING_LABEL[m] })),
            value: bk.mode,
            onChange: (v) => {
              setDraftPath("backing.mode", v);
              if (v === "loop" && bk.loop_size_bytes == null)
                setDraftPath("backing.loop_size_bytes", DEFAULT_LOOP_BYTES);
              if (v !== "loop") setDraftPath("backing.loop_size_bytes", null);
            },
          }),
        })}
        ${bk.mode === "none" &&
        html`<div class="muted" style="font-size:var(--fs-sm)">不使用回写后端，压缩页全部保留在内存中。</div>`}
        ${bk.mode === "block" &&
        html`<div class="stack stack--sm">
          ${KV({ k: "块设备", mono: true, children: BACKING_BLOCK_DEV })}
          <div class="muted" style="font-size:var(--fs-sm)">固定的默认块设备，不可修改。</div>
        </div>`}
        ${bk.mode === "loop" &&
        html`<div class="stack stack--sm">
          ${RangeField({
            label: "LOOP 文件大小",
            min: 1,
            max: MAX_LOOP_GIB,
            step: 1,
            value: loopGiB,
            valueText: `${loopGiB} GiB`,
            hint: "模块在自身目录维护该回写文件并挂载为 LOOP 设备",
            error: err("backing.loop_size_bytes"),
            onChange: (v) => setDraftPath("backing.loop_size_bytes", v * GiB),
          })}
        </div>`}
      </div>
    <//>

    <${ApplyResult} />
  </div>`;
}
