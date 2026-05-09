<script lang="ts">
  import { app } from "$lib/store.svelte";
  import { logout } from "$lib/api";
  import { seedMock, clearMock } from "$lib/mockSeed";
  import AssetsTab from "./AssetsTab.svelte";
  import PositionsTab from "./PositionsTab.svelte";
  import StatusDot from "./StatusDot.svelte";

  let { onLogout }: { onLogout: () => void } = $props();

  type Tab = "assets" | "positions";
  let activeTab: Tab = $state("assets");

  const isDev = import.meta.env.DEV;
  let mockOn = $state(false);

  function toggleMock() {
    if (mockOn) {
      clearMock();
      mockOn = false;
    } else {
      seedMock();
      mockOn = true;
    }
  }

  async function handleLogout() {
    if (!app.demo) {
      await logout();
    }
    onLogout();
  }
</script>

<div class="min-h-screen flex flex-col">
  {#if app.demo}
    <div
      class="bg-[color:oklch(0.5_0.15_70_/_15%)] border-b border-[color:oklch(0.7_0.18_70_/_30%)] text-sm"
    >
      <div class="mx-auto max-w-7xl px-6 py-2 flex items-center gap-2 text-[color:oklch(0.85_0.12_70)]">
        <span class="font-medium">Демо-режим</span>
        <span class="text-muted-foreground">
          · Синтетические данные, без подключения к Bybit. Чтобы увидеть свой
          реальный аккаунт — настрой бэкенд по README.
        </span>
      </div>
    </div>
  {/if}

  <header class="border-b">
    <div class="mx-auto max-w-7xl flex h-14 items-center px-6">
      <div class="flex items-center gap-6">
        <h1 class="text-base font-semibold tracking-tight">Bybit Mirror</h1>
        <div class="flex items-center gap-2 text-sm text-muted-foreground">
          <StatusDot connected={app.status.connected && app.wsConnected} />
          <span>
            {#if app.demo}
              Demo
            {:else if app.status.connected && app.wsConnected}
              Connected
            {:else if !app.wsConnected}
              Reconnecting…
            {:else}
              {app.status.lastError || "Disconnected"}
            {/if}
          </span>
        </div>
      </div>
      <div class="ml-auto flex items-center gap-2">
        {#if isDev && !app.demo}
          <button
            onclick={toggleMock}
            class="inline-flex items-center justify-center rounded-md text-xs font-medium h-8 px-3 border bg-transparent hover:bg-accent hover:text-accent-foreground transition-colors"
            title="Dev only: подменяет данные на фейковые для проверки UI"
          >
            {mockOn ? "Очистить mock" : "Заполнить mock"}
          </button>
        {/if}
        <button
          onclick={handleLogout}
          class="inline-flex items-center justify-center rounded-md text-xs font-medium h-8 px-3 text-muted-foreground hover:text-foreground transition-colors"
        >
          {app.demo ? "Выйти из демо" : "Выйти"}
        </button>
      </div>
    </div>

    <div class="mx-auto max-w-7xl px-6">
      <nav class="flex gap-1 -mb-px">
        <button
          onclick={() => (activeTab = "assets")}
          class="relative px-4 py-2.5 text-sm font-medium transition-colors {activeTab === 'assets'
            ? 'text-foreground'
            : 'text-muted-foreground hover:text-foreground'}"
        >
          Активы
          {#if activeTab === "assets"}
            <span class="absolute inset-x-0 bottom-0 h-0.5 bg-foreground"></span>
          {/if}
        </button>
        <button
          onclick={() => (activeTab = "positions")}
          class="relative px-4 py-2.5 text-sm font-medium transition-colors {activeTab === 'positions'
            ? 'text-foreground'
            : 'text-muted-foreground hover:text-foreground'}"
        >
          Позиции
          <span class="ml-2 inline-flex items-center justify-center min-w-5 h-5 px-1.5 bg-secondary text-xs font-medium rounded-full text-secondary-foreground">
            {app.positions.length}
          </span>
          {#if activeTab === "positions"}
            <span class="absolute inset-x-0 bottom-0 h-0.5 bg-foreground"></span>
          {/if}
        </button>
      </nav>
    </div>
  </header>

  <main class="flex-1">
    <div class="mx-auto max-w-7xl px-6 py-6">
      {#if activeTab === "assets"}
        <AssetsTab />
      {:else}
        <PositionsTab />
      {/if}
    </div>
  </main>
</div>
