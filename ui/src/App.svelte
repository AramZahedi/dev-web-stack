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
  import { initSession, refreshDaemon } from './lib/stores';
  import { openEvents } from './lib/api';

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
    void initSession();
    void refreshDaemon();
    const closeEvents = openEvents((event) => {
      if (/^(site|runtime|doctor|trust|service)\./.test(event.type)) {
        window.dispatchEvent(new CustomEvent('devstack:refresh'));
      }
    });
    return closeEvents;
  });
</script>

<AppShell>
  <Router {routes} />
</AppShell>
