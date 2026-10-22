import '@fontsource/ibm-plex-sans/latin-300.css';
import '@fontsource/ibm-plex-sans/latin-400.css';
import '@fontsource/ibm-plex-sans/latin-500.css';
import '@fontsource/ibm-plex-sans/latin-600.css';
import '@fontsource/ibm-plex-sans/latin-700.css';
import '@fontsource/jetbrains-mono/latin-400.css';
import '@fontsource/jetbrains-mono/latin-500.css';
import '@fontsource/jetbrains-mono/latin-600.css';
import '@fontsource/jetbrains-mono/latin-700.css';
import './app.css';
import { mount } from 'svelte';
import App from './App.svelte';
import { initTheme } from './lib/theme';

initTheme();

const target = document.getElementById('app');
if (!target) throw new Error('Missing #app mount point');

const app = mount(App, { target });

export default app;
