<script lang="ts">
  import { app } from "$lib/store.svelte";
  import { fmtNum } from "$lib/utils";

  const upl = $derived(app.wallet ? parseFloat(app.wallet.totalPerpUPL) : 0);
  const uplClass = $derived(
    upl > 0 ? "text-[color:var(--up)]" : upl < 0 ? "text-[color:var(--down)]" : ""
  );
  const uplSign = $derived(upl > 0 ? "+" : "");
</script>

{#if !app.wallet}
  <p class="text-sm text-muted-foreground">Загрузка кошелька…</p>
{:else}
  <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4 mb-6">
    <div class="rounded-xl border bg-card text-card-foreground shadow-sm p-5">
      <div class="text-sm text-muted-foreground">Total Equity (USD)</div>
      <div class="num text-2xl font-semibold mt-1.5">{fmtNum(app.wallet.totalEquity)}</div>
    </div>
    <div class="rounded-xl border bg-card text-card-foreground shadow-sm p-5">
      <div class="text-sm text-muted-foreground">Wallet Balance</div>
      <div class="num text-2xl font-semibold mt-1.5">{fmtNum(app.wallet.totalWalletBalance)}</div>
    </div>
    <div class="rounded-xl border bg-card text-card-foreground shadow-sm p-5">
      <div class="text-sm text-muted-foreground">Available</div>
      <div class="num text-2xl font-semibold mt-1.5">{fmtNum(app.wallet.totalAvailableBalance)}</div>
    </div>
    <div class="rounded-xl border bg-card text-card-foreground shadow-sm p-5">
      <div class="text-sm text-muted-foreground">Unrealised PnL</div>
      <div class="num text-2xl font-semibold mt-1.5 {uplClass}">
        {uplSign}{fmtNum(upl)}
      </div>
    </div>
  </div>

  <div class="rounded-xl border bg-card text-card-foreground shadow-sm">
    <div class="flex flex-col space-y-1.5 p-6 pb-4">
      <h3 class="text-base font-semibold leading-none tracking-tight">Монеты</h3>
      <p class="text-sm text-muted-foreground">
        {app.wallet.coin.length}
        {app.wallet.coin.length === 1 ? "позиция" : "позиций"} в кошельке
      </p>
    </div>
    <div class="px-0 pb-2">
      {#if app.wallet.coin.length === 0}
        <p class="px-6 py-12 text-center text-sm text-muted-foreground">
          Нет монет на счёте
        </p>
      {:else}
        <div class="relative w-full overflow-auto">
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
  </div>
{/if}
