<script lang="ts">
  import { app } from "$lib/store.svelte";
  import { fmtNum } from "$lib/utils";
</script>

{#if !app.wallet}
  <p class="text-sm text-muted-foreground">Загрузка кошелька…</p>
{:else}
  <div class="rounded-xl border bg-card text-card-foreground shadow-sm">
    <div class="flex flex-col space-y-1.5 p-4 sm:p-6 pb-4">
      <h3 class="text-base font-semibold leading-none tracking-tight">Монеты</h3>
      <p class="text-sm text-muted-foreground">
        {app.wallet.coin.length}
        {app.wallet.coin.length === 1 ? "позиция" : "позиций"} в кошельке
      </p>
    </div>
    {#if app.wallet.coin.length === 0}
      <p class="px-6 py-12 text-center text-sm text-muted-foreground">
        Нет монет на счёте
      </p>
    {:else}
      <!-- Mobile: cards -->
      <div class="md:hidden flex flex-col gap-px bg-border border-t">
        {#each app.wallet.coin as c (c.coin)}
          {@const upnl = parseFloat(c.unrealisedPnl)}
          <div class="bg-card p-4">
            <div class="flex items-center justify-between mb-3">
              <span class="font-semibold text-base">{c.coin}</span>
              <div
                class="num text-right font-semibold {upnl > 0
                  ? 'text-[color:var(--up)]'
                  : upnl < 0
                    ? 'text-[color:var(--down)]'
                    : 'text-muted-foreground'}"
              >
                {upnl > 0 ? "+" : ""}{fmtNum(upnl, 4)}
              </div>
            </div>
            <div class="grid grid-cols-2 gap-x-4 gap-y-2 text-sm">
              <div class="flex justify-between">
                <span class="text-muted-foreground">Equity</span>
                <span class="num">{fmtNum(c.equity, 4)}</span>
              </div>
              <div class="flex justify-between">
                <span class="text-muted-foreground">USD Value</span>
                <span class="num">{fmtNum(c.usdValue)}</span>
              </div>
              <div class="flex justify-between col-span-2">
                <span class="text-muted-foreground">Wallet Balance</span>
                <span class="num">{fmtNum(c.walletBalance, 4)}</span>
              </div>
            </div>
          </div>
        {/each}
      </div>

      <!-- Desktop: table -->
      <div class="hidden md:block relative w-full overflow-auto">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b text-muted-foreground">
              <th class="h-10 px-6 text-left align-middle font-medium">Монета</th>
              <th class="h-10 px-4 text-right align-middle font-medium">Equity</th>
              <th class="h-10 px-4 text-right align-middle font-medium">Wallet Balance</th>
              <th class="h-10 px-4 text-right align-middle font-medium">USD Value</th>
              <th class="h-10 px-6 text-right align-middle font-medium">Unrealised PnL</th>
            </tr>
          </thead>
          <tbody>
            {#each app.wallet.coin as c (c.coin)}
              {@const upnl = parseFloat(c.unrealisedPnl)}
              <tr class="border-b last:border-b-0 transition-colors hover:bg-accent/50">
                <td class="px-6 py-3 font-medium">{c.coin}</td>
                <td class="px-4 py-3 num text-right">{fmtNum(c.equity, 4)}</td>
                <td class="px-4 py-3 num text-right">{fmtNum(c.walletBalance, 4)}</td>
                <td class="px-4 py-3 num text-right">{fmtNum(c.usdValue)}</td>
                <td
                  class="px-6 py-3 num text-right {upnl > 0
                    ? 'text-[color:var(--up)]'
                    : upnl < 0
                      ? 'text-[color:var(--down)]'
                      : 'text-muted-foreground'}"
                >
                  {upnl > 0 ? "+" : ""}{fmtNum(upnl, 4)}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </div>
{/if}
