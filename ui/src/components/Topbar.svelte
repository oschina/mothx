<script>
  import { route } from '../lib/router.js';
  import { t } from '../lib/preferences.js';
  import { sidebarOpen, isMobile, sessions, currentSession } from '../lib/stores.js';

  function toggleSidebar() {
    sidebarOpen.update((v) => !v);
  }

  const titles = {
    chat: 'nav.newChat',
    sessions: 'nav.sessions',
    experts: 'nav.experts',
    skills: 'nav.skills',
    knowledge: 'knowledge.title',
    stats: 'nav.stats',
    cron: 'nav.cron',
    settings: 'nav.settings'
  };

  const subtitles = {
    chat: 'topbar.chat.subtitle',
    sessions: 'topbar.sessions.subtitle',
    experts: 'topbar.experts.subtitle',
    skills: 'topbar.skills.subtitle',
    knowledge: 'knowledge.subtitle',
    stats: 'topbar.stats.subtitle',
    cron: 'topbar.cron.subtitle',
    settings: 'topbar.settings.subtitle'
  };

  $: title = titles[$route.section] ? $t(titles[$route.section]) : $route.section;
  $: subtitle = subtitles[$route.section] ? $t(subtitles[$route.section]) : '';
  $: session = ($sessions || []).find((item) => item?.id === $currentSession);
</script>

<header class="topbar">
  {#if $isMobile}
    <button
      type="button"
      class="menu-toggle"
      aria-label={$t('sidebar.menu') || 'Menu'}
      aria-expanded={$sidebarOpen}
      on:click={toggleSidebar}
    >
      <span class="menu-bar"></span>
      <span class="menu-bar"></span>
      <span class="menu-bar"></span>
    </button>
  {/if}
  <div class="tb-title">
    <h1>{title}</h1>
    {#if subtitle}<span>{subtitle}</span>{/if}
  </div>
  {#if $route.section === 'chat' && session}
    <div class="topbar-session-binding" title={session.channelId || ''}>
      <span>{session.id}</span>
      <span class="session-badge">{session.channelLabel || $t('sessions.local')}</span>
    </div>
  {/if}
</header>
