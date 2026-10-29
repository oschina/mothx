<script>
  import { onDestroy, onMount } from 'svelte';
  import { get } from 'svelte/store';
  import Sidebar from './components/Sidebar.svelte';
  import Topbar from './components/Topbar.svelte';
  import Banners from './components/Banners.svelte';
  import { route, navigate, replaceNavigate } from './lib/router.js';
  import { refreshAll, connectLogs, disconnectLogs, connectRuns, disconnectRuns, currentSession, status, channels, serveConfig, isMobile, sidebarCollapsed } from './lib/stores.js';
  import { startSessionStreamDispatcher, stopSessionStreamDispatcher } from './lib/session-stream.js';
  import { hydrateWorkbench } from './lib/workbench/store.js';
  import { t } from './lib/preferences.js';

  const viewLoaders = {
    chat: () => import('./views/Chat.svelte'),
    sessions: () => import('./views/Sessions.svelte'),
    experts: () => import('./views/Experts.svelte'),
    stats: () => import('./views/Stats.svelte'),
    cron: () => import('./views/Cron.svelte'),
    skills: () => import('./views/Skills.svelte'),
    knowledge: () => import('./views/Knowledge.svelte'),
    settings: () => import('./views/Settings.svelte')
  };

  let stopRouteSync = null;
  let stopSessionSync = null;
  let authChecked = false;
  let authenticated = false;
  let appStarted = false;
  let Login = null;
  let activeView = null;
  let viewLoading = false;
  let viewLoadFailed = false;
  let viewLoadGeneration = 0;
  let loadedSection = '';
  let destroyed = false;
  $: gridTemplate = $isMobile
    ? 'minmax(0, 1fr)'
    : ($sidebarCollapsed ? '56px minmax(0, 1fr)' : '280px minmax(0, 1fr)');

  onMount(async () => {
    try {
      const auth = await fetch('/api/auth/status').then(async (response) => {
        const data = await response.json();
        if (!response.ok) throw new Error(data?.error?.message || data?.message || 'Authentication status unavailable');
        return data;
      });
      authenticated = auth?.authenticated === true;
    } catch {
      authenticated = false;
    } finally {
      authChecked = true;
      if (authenticated) {
        startApp();
      } else {
        loadLogin();
      }
    }
  });

  async function loadLogin() {
    try {
      const module = await import('./views/Login.svelte');
      if (!destroyed) Login = module.default;
    } catch {
      // Keep the loading state visible if the login chunk cannot be loaded.
    }
  }

  function startApp() {
    if (appStarted) return;
    appStarted = true;
    // Hydrate the workbench once, before any view action can mutate it. Chat is
    // route-loaded later; re-reading localStorage there would drop layout state
    // that has not reached the persistence debounce yet.
    hydrateWorkbench();
    if (!window.location.hash) navigate('/chat');
    stopRouteSync = route.subscribe((nextRoute) => {
      syncSessionFromRoute(nextRoute);
      // Hash changes inside one section (session/view tabs) must not remount
      // the view: the workbench owns that navigation and a remount would drop
      // unsaved pane state and re-hydrate layout from a stale localStorage copy.
      if (nextRoute.section !== loadedSection) {
        loadedSection = nextRoute.section;
        loadRouteView(nextRoute.section);
      }
    });
    stopSessionSync = currentSession.subscribe(syncRouteFromSession);
    connectLogs();
    // The dispatcher owns the single /ws/runs consumer and the SSE fallback.
    // It starts before the socket so no replayed event can be dropped.
    startSessionStreamDispatcher();
    connectRuns();
    refreshAll();
  }

  async function loadRouteView(section) {
    const loader = viewLoaders[section];
    const generation = ++viewLoadGeneration;
    viewLoadFailed = false;
    activeView = null;
    if (!loader) {
      viewLoading = false;
      return;
    }
    viewLoading = true;
    try {
      const module = await loader();
      if (destroyed || generation !== viewLoadGeneration) return;
      activeView = module.default;
    } catch {
      if (destroyed || generation !== viewLoadGeneration) return;
      viewLoadFailed = true;
    } finally {
      if (!destroyed && generation === viewLoadGeneration) viewLoading = false;
    }
  }

  function handleAuthenticated() {
    authenticated = true;
    startApp();
  }

  onDestroy(() => {
    destroyed = true;
    stopRouteSync?.();
    stopSessionSync?.();
    disconnectLogs();
    disconnectRuns();
    stopSessionStreamDispatcher();
  });

  function syncSessionFromRoute(nextRoute) {
    if (nextRoute.section !== 'chat') return;
    const routeSession = nextRoute.query?.session || '';
    if (get(currentSession) !== routeSession) currentSession.set(routeSession);
  }

  function syncRouteFromSession(sessionID) {
    const currentRoute = get(route);
    if (currentRoute.section !== 'chat') return;
    const routeSession = currentRoute.query?.session || '';
    const nextSession = sessionID || '';
    if (routeSession === nextSession) return;
    replaceNavigate(nextSession ? `/chat?session=${encodeURIComponent(nextSession)}` : '/chat');
  }
</script>

{#if !authChecked}
  <div class="login-loading"><span class="spinner lg"></span></div>
{:else if !authenticated}
  {#if Login}
    <svelte:component this={Login} on:authenticated={handleAuthenticated} />
  {:else}
    <div class="login-loading"><span class="spinner lg"></span></div>
  {/if}
{:else}
<div class="app-shell" class:sidebar-is-collapsed={!$isMobile && $sidebarCollapsed} style={`grid-template-columns: ${gridTemplate}`}>
  <Sidebar />
  <main class="workbench">
    {#if $route.section !== 'chat'}
      <Topbar />
    {/if}
    <Banners />
    <div class="view-container">
      {#if viewLoading}
        <div class="login-loading"><span class="spinner lg"></span></div>
      {:else if viewLoadFailed}
        <section class="page">
          <p class="empty">{$t('app.unknownPage')}: {$route.path}</p>
        </section>
      {:else if activeView}
        <svelte:component this={activeView} />
      {:else}
        <section class="page">
          <p class="empty">{$t('app.unknownPage')}: {$route.path}</p>
        </section>
      {/if}
    </div>
  </main>
</div>
{/if}
