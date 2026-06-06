// Infra portal entry point. Mounts InfraApp.svelte on #app.
// Served by the Go binary on the --infra-addr listener (:8089 by
// convention) ; WireGuard-mesh-only cluster-wide ops.
import { mount } from 'svelte';
import '../app.css';
import InfraApp from './InfraApp.svelte';

mount(InfraApp, { target: document.getElementById('app')! });
