<script lang="ts">
  import { onMount } from 'svelte';
  import Router from 'svelte-spa-router';
  import AppShell from './lib/components/AppShell.svelte';
  import Sites from './routes/Sites.svelte';
  import AddSite from './routes/AddSite.svelte';
  import SiteDetail from './routes/SiteDetail.svelte';
  import Runtimes from './routes/Runtimes.svelte';
  import Services from './routes/Services.svelte';
  import Doctor from './routes/Doctor.svelte';
  import Settings from './routes/Settings.svelte';
  import NotFound from './routes/NotFound.svelte';
  import { initSession, refreshDaemon, session } from './lib/stores';
  import { authenticateFromFragment, openEvents } from './lib/api';

  const routes = {
    '/': Sites,
    '/sites': Sites,
    '/sites/new': AddSite,
    '/sites/:ref': SiteDetail,
    '/services': Services,
    '/runtimes': Runtimes,
    '/doctor': Doctor,
    '/settings': Settings,
    '*': NotFound,
  };

  onMount(() => {
    let cancelled = false;
    let closeEvents = () => {};
    void (async () => {
      try {
        await authenticateFromFragment();
      } catch {
        // The session state below surfaces the locked dashboard; the token in
        // the fragment is one-shot and has already been removed from history.
      }
      if (cancelled) return;
      await initSession();
      if (cancelled) return;
      void refreshDaemon();
      // Do not open the SSE stream while the dashboard is locked: EventSource
      // would retry the 403 forever. The stream starts only after the install
      // token has established an authenticated session.
      if ($session.authenticated) {
        closeEvents = openEvents((event) => {
          if (/^(site|runtime|doctor|trust|service)\./.test(event.type)) {
            window.dispatchEvent(new CustomEvent('devstack:refresh'));
          }
        });
      }
    })();
    return () => {
      cancelled = true;
      closeEvents();
    };
  });
</script>

<AppShell>
  <Router {routes} />
</AppShell>
