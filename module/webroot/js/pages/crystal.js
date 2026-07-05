/* pages/crystal.js — Crystal Hybridswap policy editor. */

import { html } from "../../vendor/preact.mjs";
import { useStore, setDraftPath } from "../store.js";
import { Card, TriToggleRow, OptNumberField, OptSizeField } from "../components/ui.js";
import { SaveBar } from "../components/savebar.js";
import { fmtBytes } from "../format.js";

export function CrystalPolicy() {
  const { draft, errors, status } = useStore((s) => ({
    draft: s.draft,
    errors: s.draftErrors,
    status: s.status,
  }));
  if (!draft) return null;
  const c = draft.crystal || {};
  const sys = (status && status.crystal) || {};
  const err = (p) => (errors ? errors[p] : null);

  return html`<div class="stack fade-in">
    <div>
      <div class="page-title">策略</div>
      <div class="page-sub">留空即"跟随系统"，模块不接管；设置后由守护进程持续维护。</div>
    </div>

    <${SaveBar} />

    <${Card} title="核心开关" icon="power">
      <div class="stack">
        ${TriToggleRow({ label: "启用 Hybridswap", desc: "hybridswap_enable", value: c.enable, sysValue: sys.enable, onChange: (v) => setDraftPath("crystal.enable", v) })}
        ${TriToggleRow({ label: "核心回收 (core_enable)", desc: "reclaim_in", value: c.core_enable, sysValue: sys.core_enable, onChange: (v) => setDraftPath("crystal.core_enable", v) })}
        ${TriToggleRow({ label: "暂停 swapd", desc: "swapd_pause", value: c.swapd_pause, sysValue: sys.swapd_pause, onChange: (v) => setDraftPath("crystal.swapd_pause", v) })}
      </div>
    <//>

    <${Card} title="日志与寿命" icon="sliders">
      <div class="stack">
        ${OptNumberField({ label: "日志级别 (loglevel)", value: c.loglevel, sysValue: sys.loglevel, min: 0, max: 3, error: err("crystal.loglevel"), onChange: (v) => setDraftPath("crystal.loglevel", v) })}
        ${OptNumberField({
          label: "设备寿命保护等级 (dev_life)",
          hint: "0 关闭保护；1/2/3 逐级降低写回预算。",
          value: c.dev_life,
          sysValue: sys.dev_life,
          min: 0,
          max: 3,
          error: err("crystal.dev_life"),
          onChange: (v) => setDraftPath("crystal.dev_life", v),
        })}
      </div>
    <//>

    <${Card} title="配额与增长" icon="snow">
      <div class="stack">
        ${OptSizeField({
          label: "日回写配额",
          hint: "支持 K/M/G/T 单位（如 10G、1024M），纯数字为字节，0 表示无限制",
          value: c.quota_day,
          sysValue: sys.quota_day,
          placeholder:
            sys.quota_day == null
              ? "跟随系统"
              : `系统 ${Number(sys.quota_day) === 0 ? "无限制" : fmtBytes(sys.quota_day)}`,
          error: err("crystal.quota_day"),
          onChange: (v) => setDraftPath("crystal.quota_day", v),
        })}
        ${OptNumberField({
          label: "ZRAM 压力增量",
          hint: "用于提高自动回写时看到的 ZRAM 压力，并增加本轮回写预算。",
          value: c.zram_increase,
          sysValue: sys.zram_increase,
          min: 0,
          max: 1048576,
          suffix: "MiB",
          error: err("crystal.zram_increase"),
          onChange: (v) => setDraftPath("crystal.zram_increase", v),
        })}
      </div>
    <//>
  </div>`;
}
