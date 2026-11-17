import { MOQtailClient } from "moqtail/client";

export type RelaySession = {
  ready: Promise<void>;
  disconnect: () => Promise<void>;
};

export function connectRelay(
  url: string,
  onDisconnected: () => void,
): RelaySession {
  const client = new MOQtailClient({ url });
  client.on("sessionterminated", onDisconnected);

  return {
    ready: client.init(),
    disconnect: () => client.disconnect(),
  };
}
