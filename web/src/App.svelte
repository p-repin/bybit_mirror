<script lang="ts">
  import { onMount } from "svelte";
  import { snapshot } from "$lib/api";
  import { app, applySnapshot, connectWS, disconnectWS } from "$lib/store.svelte";
  import { seedMock, clearMock } from "$lib/mockSeed";
  import LoginScreen from "$lib/components/LoginScreen.svelte";
  import Dashboard from "$lib/components/Dashboard.svelte";

  let bootError: string | null = $state(null);

  async function tryBoot() {
    bootError = null;
    try {
      const snap = await snapshot();
      applySnapshot(snap);
      app.authed = true;
      connectWS();
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      if (msg === "unauthorized") {
        app.authed = false;
      } else {
        bootError = msg;
      }
    } finally {
      app.booting = false;
    }
  }

  function handleLogin() {
    tryBoot();
  }

  function handleDemo() {
    seedMock();
    app.demo = true;
    app.authed = true;
    app.wsConnected = true;
    app.status = { connected: true, updatedAt: new Date().toISOString() };
  }

  function handleLogout() {
    if (app.demo) {
      clearMock();
      app.demo = false;
    } else {
      disconnectWS();
    }
    app.authed = false;
    app.wallet = null;
    app.positions = [];
    app.wsConnected = false;
    app.status = { connected: false, updatedAt: "" };
  }

  onMount(tryBoot);
</script>

{#if app.booting}
  <div class="min-h-screen"></div>
{:else if app.authed}
  <Dashboard onLogout={handleLogout} />
{:else}
  <LoginScreen onLogin={handleLogin} onDemo={handleDemo} {bootError} />
{/if}
