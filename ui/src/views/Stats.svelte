<script>
  import { onDestroy, onMount } from 'svelte';
  import {
    getStatsSummary,
    getStatsTimeSeries,
    getStatsByProvider,
    getStatsByModel,
    getStatsRecent,
    refreshStatsSummary,
    isMobile
  } from '../lib/stores.js';
  import { t } from '../lib/preferences.js';
  import { RefreshCw } from '@lucide/svelte';

  let range = 'week';
  let loading = false;
  let error = '';
  let summary = {};
  let timeseries = [];
  let byProvider = [];
  let byModel = [];
  let recent = { items: [], total: 0, page: 1, pageSize: 12 };
  let recentLoading = false;
  const recentPageSize = 12;
  let trendCanvas;
  let trendFrame = 0;
  let resizeObserver;

  $: query = rangeParams(range);
  $: trendSeries = buildTrendSeries(timeseries, range, query);
  $: trendTotalTokens = trendSeries.reduce((sum, item) => sum + Number(item.totalTokens || 0), 0);
  $: displayTrendTotal = trendTotalTokens || Number(summary.totalTokens || 0);
  $: maxProvider = Math.max(1, ...byProvider.map((item) => Number(item.totalTokens || 0)));
  $: maxModel = Math.max(1, ...byModel.map((item) => Number(item.totalTokens || 0)));
  $: recentTotalPages = Math.max(1, Math.ceil(Number(recent.total || 0) / Number(recent.pageSize || recentPageSize)));
  $: recentPageNumbers = pageNumbers(Number(recent.page || 1), recentTotalPages);
  $: {
    trendSeries;
    trendCanvas;
    scheduleTrendDraw();
  }

  onMount(() => {
    loadStats();
    if (typeof ResizeObserver !== 'undefined' && trendCanvas?.parentElement) {
      resizeObserver = new ResizeObserver(() => scheduleTrendDraw());
      resizeObserver.observe(trendCanvas.parentElement);
    }
  });

  onDestroy(() => {
    if (trendFrame) cancelAnimationFrame(trendFrame);
    resizeObserver?.disconnect();
  });

  async function loadStats() {
    loading = true;
    error = '';
    try {
      const params = { ...query };
      const [nextSummary, nextSeries, nextProviders, nextModels, nextRecent] = await Promise.all([
        getStatsSummary(params),
        getStatsTimeSeries({ ...params, groupBy: range === 'today' ? '1h' : 'day' }),
        getStatsByProvider(params),
        getStatsByModel(params),
        getStatsRecent({ ...params, page: 1, pageSize: recentPageSize })
      ]);
      summary = nextSummary || {};
      timeseries = Array.isArray(nextSeries) ? nextSeries : [];
      byProvider = sortByTotalTokens(nextProviders);
      byModel = sortByTotalTokens(nextModels);
      recent = normalizeRecent(nextRecent);
      await refreshStatsSummary();
    } catch (err) {
      error = err instanceof Error ? err.message : String(err || $t('stats.loadFailed'));
    } finally {
      loading = false;
    }
  }

  async function loadRecentPage(page) {
    const totalPages = recentTotalPages || 1;
    const nextPage = Math.min(Math.max(1, Number(page || 1)), totalPages);
    if (recentLoading || nextPage === Number(recent.page || 1)) return;
    recentLoading = true;
    error = '';
    try {
      recent = normalizeRecent(await getStatsRecent({ ...query, page: nextPage, pageSize: recentPageSize }));
    } catch (err) {
      error = err instanceof Error ? err.message : String(err || $t('stats.loadFailed'));
    } finally {
      recentLoading = false;
    }
  }

  function normalizeRecent(value) {
    return value || { items: [], total: 0, page: 1, pageSize: recentPageSize };
  }

  function sortByTotalTokens(value) {
    if (!Array.isArray(value)) return [];
    return [...value].sort((a, b) => Number(b?.totalTokens || 0) - Number(a?.totalTokens || 0));
  }

  function rangeParams(value) {
    if (value === 'all') return {};
    const today = new Date();
    const end = dateKey(today);
    const start = new Date(today);
    if (value === 'today') {
      return { from: end, to: end };
    }
    if (value === 'month') {
      start.setDate(today.getDate() - 29);
      return { from: dateKey(start), to: end };
    }
    start.setDate(today.getDate() - 6);
    return { from: dateKey(start), to: end };
  }

  function dateKey(date) {
    const y = date.getFullYear();
    const m = String(date.getMonth() + 1).padStart(2, '0');
    const d = String(date.getDate()).padStart(2, '0');
    return `${y}-${m}-${d}`;
  }

  function parseDateKey(value) {
    const parts = String(value || '').split('-').map((part) => Number(part));
    if (parts.length !== 3 || parts.some((part) => !Number.isFinite(part))) return null;
    return new Date(parts[0], parts[1] - 1, parts[2]);
  }

  function buildTrendSeries(raw, value, params) {
    const source = Array.isArray(raw) ? raw : [];
    if (source.length === 0 && value === 'all') return [];
    const byLabel = new Map(source.map((item) => [item.label, item]));
    if (value === 'today' && params?.from) {
      const items = [];
      for (let hour = 0; hour < 24; hour += 1) {
        const label = `${params.from} ${String(hour).padStart(2, '0')}:00`;
        items.push(byLabel.get(label) || { label, totalTokens: 0, inputTokens: 0, outputTokens: 0, requests: 0 });
      }
      return items;
    }
    if ((value === 'week' || value === 'month') && params?.from && params?.to) {
      const start = parseDateKey(params.from);
      const end = parseDateKey(params.to);
      if (!start || !end) return source;
      const items = [];
      for (let day = new Date(start); day <= end; day.setDate(day.getDate() + 1)) {
        const label = dateKey(day);
        items.push(byLabel.get(label) || { label, totalTokens: 0, inputTokens: 0, outputTokens: 0, requests: 0 });
      }
      return items;
    }
    return source;
  }

  function formatNumber(value) {
    return new Intl.NumberFormat().format(Number(value || 0));
  }

  function formatCompact(value) {
    const n = Number(value || 0);
    if (Math.abs(n) < 10000) return formatNumber(n);
    return new Intl.NumberFormat(undefined, {
      notation: 'compact',
      maximumFractionDigits: 1
    }).format(n);
  }

  function percent(value, max) {
    return `${Math.max(3, Math.round((Number(value || 0) / max) * 100))}%`;
  }

  function scheduleTrendDraw() {
    if (!trendCanvas) return;
    if (trendFrame) cancelAnimationFrame(trendFrame);
    trendFrame = requestAnimationFrame(() => {
      trendFrame = 0;
      drawTrendChart(trendCanvas, trendSeries);
    });
  }

  function drawTrendChart(canvas, series) {
    if (!canvas) return;
    const ctx = canvas.getContext('2d');
    const parent = canvas.parentElement;
    if (!ctx || !parent) return;
    const styles = getComputedStyle(document.documentElement);
    const color = (name, fallback) => styles.getPropertyValue(name).trim() || fallback;
    const dpr = window.devicePixelRatio || 1;
    const rect = parent.getBoundingClientRect();
    const width = Math.max(1, rect.width);
    const height = Math.max(1, rect.height);
    canvas.width = Math.floor(width * dpr);
    canvas.height = Math.floor(height * dpr);
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, width, height);

    const values = (series || []).map((item) => Number(item.totalTokens || 0));
    const labels = (series || []).map((item) => String(item.label || ''));
    const textMuted = color('--text-muted', '#6b7280');
    if (!values.length || !values.some((value) => value > 0)) {
      ctx.fillStyle = textMuted;
      ctx.textAlign = 'center';
      ctx.font = '13px system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif';
      ctx.fillText($t('stats.empty'), width / 2, height / 2);
      return;
    }

    const padding = $isMobile
      ? { top: 12, right: 10, bottom: 32, left: 38 }
      : { top: 18, right: 18, bottom: 38, left: 54 };
    const chartWidth = Math.max(1, width - padding.left - padding.right);
    const chartHeight = Math.max(1, height - padding.top - padding.bottom);
    const maxValue = niceCeil(Math.max(...values));
    const gridLines = 5;

    ctx.strokeStyle = color('--border-subtle', '#e5e7eb');
    ctx.lineWidth = 1;
    ctx.font = '11px system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif';
    for (let i = 0; i <= gridLines; i += 1) {
      const y = padding.top + (chartHeight / gridLines) * i;
      ctx.beginPath();
      ctx.moveTo(padding.left, y);
      ctx.lineTo(width - padding.right, y);
      ctx.stroke();

      const value = maxValue - (maxValue / gridLines) * i;
      ctx.fillStyle = textMuted;
      ctx.textAlign = 'right';
      ctx.fillText(formatAxisTick(value), padding.left - 8, y + 4);
    }

    // Keep bars proportional to their day slot but cap the width so a short
    // range reads as distinct columns instead of one solid block.
    const slotWidth = chartWidth / values.length;
    const dense = values.length > 60;
    const barWidth = dense
      ? Math.max(2, slotWidth - 1)
      : Math.min(30, Math.max(6, slotWidth * 0.6));
    const accent = color('--accent', '#22c55e');
    const accentDeep = shadeHex(accent, 0.72);
    for (let i = 0; i < values.length; i += 1) {
      const barHeight = values[i] > 0 ? (values[i] / maxValue) * chartHeight : 0;
      if (barHeight <= 0) continue;
      const slotStart = padding.left + i * slotWidth;
      const x = slotStart + (slotWidth - barWidth) / 2;
      if (x > width - padding.right) break;
      const y = padding.top + chartHeight - barHeight;
      const drawWidth = Math.min(barWidth, width - padding.right - x);
      const gradient = ctx.createLinearGradient(x, y, x, padding.top + chartHeight);
      gradient.addColorStop(0, accent);
      gradient.addColorStop(1, accentDeep);
      ctx.fillStyle = gradient;
      ctx.beginPath();
      const radius = Math.min(4, drawWidth / 2);
      if (ctx.roundRect && barHeight > radius * 2) {
        ctx.roundRect(x, y, drawWidth, barHeight, [radius, radius, 0, 0]);
      } else {
        ctx.rect(x, y, drawWidth, barHeight);
      }
      ctx.fill();
    }

    ctx.fillStyle = textMuted;
    ctx.textAlign = 'center';
    const labelCount = Math.min(6, labels.length);
    const labelStep = Math.max(1, Math.floor(labels.length / labelCount));
    for (let i = 0; i < labels.length; i += labelStep) {
      const x = padding.left + i * slotWidth + slotWidth / 2;
      if (x > width - padding.right + 8) break;
      ctx.fillText(axisLabel(labels[i]), x, height - padding.bottom + 20);
    }
  }

  // Round the axis maximum to a 1/2/2.5/5/10 step so five grid lines land on
  // readable values instead of arbitrary fractions.
  function niceCeil(value) {
    const n = Number(value || 0);
    if (!(n > 0)) return 1;
    const exponent = Math.floor(Math.log10(n));
    const base = Math.pow(10, exponent);
    const fraction = n / base;
    const step = fraction <= 1 ? 1 : fraction <= 2 ? 2 : fraction <= 2.5 ? 2.5 : fraction <= 5 ? 5 : 10;
    return step * base;
  }

  // Compact, unit-stable axis ticks: avoids mixing "3.8亿" and "7500.6万"
  // on the same scale while staying locale-aware.
  function formatAxisTick(value) {
    const n = Number(value || 0);
    return new Intl.NumberFormat(undefined, {
      notation: 'compact',
      maximumFractionDigits: n >= 100000000 ? 1 : 0
    }).format(n);
  }

  function shadeHex(hex, factor) {
    const match = /^#([0-9a-f]{6})$/i.exec(String(hex || '').trim());
    if (!match) return hex;
    const value = parseInt(match[1], 16);
    const channel = (shift) => Math.max(0, Math.min(255, Math.round(((value >> shift) & 255) * factor)));
    return `rgb(${channel(16)}, ${channel(8)}, ${channel(0)})`;
  }

  function axisLabel(label) {
    if (!label) return '';
    if (label.length > 10) return label.slice(5);
    if (/^\d{4}-\d{2}-\d{2}$/.test(label)) return label.slice(5);
    return label;
  }

  function pageNumbers(page, total) {
    const maxVisible = 5;
    const totalPages = Math.max(1, total || 1);
    let start = Math.max(1, page - Math.floor(maxVisible / 2));
    let end = Math.min(totalPages, start + maxVisible - 1);
    if (end - start + 1 < maxVisible) start = Math.max(1, end - maxVisible + 1);
    const pages = [];
    for (let p = start; p <= end; p += 1) pages.push(p);
    return pages;
  }

  function duration(ms) {
    const n = Number(ms || 0);
    if (n < 1000) return `${n}ms`;
    return `${(n / 1000).toFixed(1)}s`;
  }

  function timeLabel(value) {
    if (!value) return '';
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return '';
    return date.toLocaleString([], {
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit'
    });
  }
</script>

<section class="page stats-page">
  <div class="page-toolbar">
    <select bind:value={range} on:change={loadStats} aria-label={$t('stats.range')}>
      <option value="today">{$t('stats.range.today')}</option>
      <option value="week">{$t('stats.range.week')}</option>
      <option value="month">{$t('stats.range.month')}</option>
      <option value="all">{$t('stats.range.all')}</option>
    </select>
    <button type="button" class="stats-refresh" on:click={loadStats} disabled={loading}>
      {#if loading}<span class="spinner sm"></span> {$t('common.loading')}{:else}<RefreshCw size={14} aria-hidden="true" /> {$t('common.refresh')}{/if}
    </button>
  </div>

  {#if error}
    <div class="banner banner-error">{error}</div>
  {/if}

  <div class="stats-summary">
    <div class="stats-kpi">
      <span>{$t('stats.requests')}</span>
      <strong>{formatNumber(summary.totalRequests)}</strong>
    </div>
    <div class="stats-kpi">
      <span>{$t('stats.totalTokens')}</span>
      <strong>{formatCompact(summary.totalTokens)}</strong>
    </div>
    <div class="stats-kpi">
      <span>{$t('stats.inputTokens')}</span>
      <strong>{formatCompact(summary.inputTokens)}</strong>
    </div>
    <div class="stats-kpi">
      <span>{$t('stats.outputTokens')}</span>
      <strong>{formatCompact(summary.outputTokens)}</strong>
    </div>
  </div>

  <div class="stats-grid">
    <div class="card stats-trend">
      <div class="card-head">
        <h3>{$t('stats.trend')}</h3>
        <span class="hint" title={formatNumber(displayTrendTotal)}>
          {$t('stats.totalTokens')}: {formatCompact(displayTrendTotal)}
        </span>
      </div>
      <div class="stats-chart">
        <canvas bind:this={trendCanvas} aria-label={$t('stats.trend')}></canvas>
      </div>
    </div>

    <div class="stats-side">
      <div class="card stats-rank">
        <div class="card-head">
          <h3>{$t('stats.providers')}</h3>
          <span class="hint">{$t('common.count', { count: formatNumber(byProvider.length) })}</span>
        </div>
        <div class="rank-list">
          {#each byProvider.slice(0, 8) as item}
            <div class="rank-row">
              <span>{item.vendor || item.label || '-'}</span>
              <strong>{formatCompact(item.totalTokens)}</strong>
              <div><span style={`width: ${percent(item.totalTokens, maxProvider)}`}></span></div>
            </div>
          {/each}
          {#if byProvider.length === 0}
            <p class="empty">{$t('stats.empty')}</p>
          {/if}
        </div>
      </div>

      <div class="card stats-rank">
        <div class="card-head">
          <h3>{$t('stats.models')}</h3>
          <span class="hint">{$t('common.count', { count: formatNumber(byModel.length) })}</span>
        </div>
        <div class="rank-list">
          {#each byModel.slice(0, 8) as item}
            <div class="rank-row">
              <span>{item.model || item.label || '-'}</span>
              <strong>{formatCompact(item.totalTokens)}</strong>
              <div><span style={`width: ${percent(item.totalTokens, maxModel)}`}></span></div>
            </div>
          {/each}
          {#if byModel.length === 0}
            <p class="empty">{$t('stats.empty')}</p>
          {/if}
        </div>
      </div>
    </div>
  </div>

  <div class="card">
    <div class="card-head">
      <h3>{$t('stats.recent')}</h3>
      <span class="hint">{$t('common.count', { count: formatNumber(recent.total || 0) })}</span>
    </div>
    {#if $isMobile}
      <div class="stats-cards">
        {#each recent.items || [] as item}
          <div class="stats-card-item">
            <div class="stats-card-row">
              <span class="stats-card-label">{$t('stats.time')}</span>
              <span class="stats-card-value">{timeLabel(item.timestamp)}</span>
            </div>
            <div class="stats-card-row">
              <span class="stats-card-label">{$t('stats.model')}</span>
              <span class="stats-card-value" title={item.model}>{item.model || '-'}</span>
            </div>
            <div class="stats-card-row">
              <span class="stats-card-label">{$t('stats.provider')}</span>
              <span class="stats-card-value">{item.vendor || '-'}</span>
            </div>
            <div class="stats-card-row">
              <span class="stats-card-label">{$t('stats.input')}</span>
              <span class="stats-card-value num">{formatNumber(item.inputTokens)}</span>
            </div>
            <div class="stats-card-row">
              <span class="stats-card-label">{$t('stats.output')}</span>
              <span class="stats-card-value num">{formatNumber(item.outputTokens)}</span>
            </div>
            <div class="stats-card-row">
              <span class="stats-card-label">{$t('stats.duration')}</span>
              <span class="stats-card-value num">{duration(item.durationMs)}</span>
            </div>
          </div>
        {/each}
        {#if !recent.items || recent.items.length === 0}
          <p class="empty">{$t('stats.empty')}</p>
        {/if}
      </div>
    {:else}
      <table class="table stats-table">
        <thead>
          <tr>
            <th>{$t('stats.time')}</th>
            <th>{$t('stats.model')}</th>
            <th>{$t('stats.provider')}</th>
            <th class="num">{$t('stats.input')}</th>
            <th class="num">{$t('stats.output')}</th>
            <th class="num">{$t('stats.duration')}</th>
          </tr>
        </thead>
        <tbody>
          {#each recent.items || [] as item}
            <tr>
              <td>{timeLabel(item.timestamp)}</td>
              <td class="wd">{item.model || '-'}</td>
              <td>{item.vendor || '-'}</td>
              <td class="num">{formatNumber(item.inputTokens)}</td>
              <td class="num">{formatNumber(item.outputTokens)}</td>
              <td class="num">{duration(item.durationMs)}</td>
            </tr>
          {/each}
          {#if !recent.items || recent.items.length === 0}
            <tr>
              <td colspan="6" class="empty-cell">{$t('stats.empty')}</td>
            </tr>
          {/if}
        </tbody>
      </table>
    {/if}
    {#if Number(recent.total || 0) > 0}
      <div class="stats-pagination">
        <button
          type="button"
          class="page-btn"
          title={$t('common.first')}
          disabled={recentLoading || Number(recent.page || 1) <= 1}
          on:click={() => loadRecentPage(1)}
        >«</button>
        <button
          type="button"
          class="page-btn"
          disabled={recentLoading || Number(recent.page || 1) <= 1}
          on:click={() => loadRecentPage(Number(recent.page || 1) - 1)}
        >{$t('common.previous')}</button>
        {#each recentPageNumbers as page}
          <button
            type="button"
            class="page-btn"
            class:active={page === Number(recent.page || 1)}
            disabled={recentLoading}
            on:click={() => loadRecentPage(page)}
          >{page}</button>
        {/each}
        <button
          type="button"
          class="page-btn"
          disabled={recentLoading || Number(recent.page || 1) >= recentTotalPages}
          on:click={() => loadRecentPage(Number(recent.page || 1) + 1)}
        >{$t('common.nextPage')}</button>
        <button
          type="button"
          class="page-btn"
          title={$t('common.last')}
          disabled={recentLoading || Number(recent.page || 1) >= recentTotalPages}
          on:click={() => loadRecentPage(recentTotalPages)}
        >»</button>
        <span class="page-info">{$t('stats.pageInfo', { page: recent.page || 1, total: recentTotalPages })}</span>
      </div>
    {/if}
  </div>
</section>
