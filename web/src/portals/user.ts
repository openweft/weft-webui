// User portal entry point. Mounts UserApp.svelte on #app.
// Vite emits this as web/dist/user/assets/user-<hash>.js, loaded by
// web/dist/user/index.html, served by the Go binary on :8080.
import { mount } from 'svelte';
import '../app.css';
import UserApp from './UserApp.svelte';

mount(UserApp, { target: document.getElementById('app')! });
