<script lang="ts">
  import { login } from "$lib/api";

  let {
    onLogin,
    onDemo,
    bootError = null,
  }: { onLogin: () => void; onDemo: () => void; bootError?: string | null } = $props();

  let password = $state("");
  let busy = $state(false);
  let error: string | null = $state(null);

  async function submit(e: Event) {
    e.preventDefault();
    if (busy || !password) return;
    busy = true;
    error = null;
    try {
      await login(password);
      onLogin();
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
    } finally {
      busy = false;
    }
  }
</script>

<div class="min-h-screen flex items-center justify-center px-4">
  <div class="w-full max-w-sm space-y-3">
    <form
      onsubmit={submit}
      class="bg-card border rounded-xl p-6 shadow-2xl"
    >
      <div class="space-y-1.5 mb-6">
        <h1 class="text-xl font-semibold tracking-tight">Bybit Mirror</h1>
        <p class="text-sm text-muted-foreground">
          Введите общий пароль для доступа к зеркалу аккаунта.
        </p>
      </div>

      <div class="space-y-2">
        <label class="text-sm font-medium leading-none" for="pw">Пароль</label>
        <input
          id="pw"
          type="password"
          bind:value={password}
          class="flex h-9 w-full rounded-md border bg-transparent px-3 py-1 text-base sm:text-sm shadow-sm transition-colors placeholder:text-muted-foreground disabled:opacity-50"
          autocomplete="current-password"
          disabled={busy}
        />
      </div>

      {#if error || bootError}
        <p class="mt-3 text-sm text-[color:var(--down)]">
          {error || bootError}
        </p>
      {/if}

      <button
        type="submit"
        disabled={busy || !password}
        class="mt-6 inline-flex w-full items-center justify-center gap-2 whitespace-nowrap rounded-md text-sm font-medium h-9 px-4 py-2 bg-primary text-primary-foreground shadow hover:bg-primary/90 disabled:pointer-events-none disabled:opacity-50 transition-colors"
      >
        {busy ? "Входим..." : "Войти"}
      </button>
    </form>

    <button
      type="button"
      onclick={onDemo}
      class="w-full inline-flex items-center justify-center gap-2 rounded-md text-sm font-medium h-9 px-4 py-2 border bg-transparent hover:bg-accent hover:text-accent-foreground transition-colors text-muted-foreground"
    >
      Посмотреть демо без подключения
    </button>
    <p class="text-xs text-muted-foreground text-center px-2">
      В демо-режиме показываются синтетические данные. Без Bybit и без бэкенда — просто чтобы посмотреть, как выглядит интерфейс.
    </p>
  </div>
</div>
