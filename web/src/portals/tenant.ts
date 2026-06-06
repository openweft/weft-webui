// Tenant portal entry point. Mounts TenantApp.svelte on #app.
// Served by the Go binary on the --tenant-addr listener (:8088 by
// convention) ; tenant-admin + regular users in their tenant.
import { mount } from 'svelte';
import '../app.css';
import TenantApp from './TenantApp.svelte';

mount(TenantApp, { target: document.getElementById('app')! });
