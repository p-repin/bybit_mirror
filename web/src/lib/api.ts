import type { Snapshot } from "./types";

export async function login(password: string): Promise<void> {
  const res = await fetch("/api/login", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ password }),
    credentials: "include",
  });
  if (!res.ok) {
    throw new Error(res.status === 401 ? "Wrong password" : `Login failed: ${res.status}`);
  }
}

export async function logout(): Promise<void> {
  await fetch("/api/logout", { method: "POST", credentials: "include" });
}

export async function snapshot(): Promise<Snapshot> {
  const res = await fetch("/api/snapshot", { credentials: "include" });
  if (res.status === 401) throw new Error("unauthorized");
  if (!res.ok) throw new Error(`Snapshot failed: ${res.status}`);
  return res.json();
}
