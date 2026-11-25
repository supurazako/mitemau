import { AuthorizationToken, SetupOptions, GroupOrder, LocationFilter, LocationFilterType, RequestError } from "moqtail";
import type { FullTrackName, MoqtObject } from "moqtail";
import { LiveTrackSource, MOQtailClient } from "moqtail/client";

export const TEST_RELAY_URL = "https://relay-test.supurazako.com:4433";

export type MediaPublication = {
  ready: Promise<void>;
  addTrack: (name: FullTrackName) => { stop: () => void };
  write: (object: MoqtObject) => void;
  stop: () => Promise<void>;
};

export type RelaySession = {
  ready: Promise<void>;
  subscribeMedia: (name: FullTrackName) => Promise<{
    stream: ReadableStream<MoqtObject>;
    unsubscribe: () => Promise<void>;
  }>;
  publishMedia: (names: readonly [FullTrackName, ...FullTrackName[]]) => MediaPublication;
  disconnect: () => Promise<void>;
};

export function connectRelay(
  url: string,
  onDisconnected: () => void,
  token?: string,
): RelaySession {
  const setupOptions = token
    ? new SetupOptions().addAuthorizationToken(AuthorizationToken.newRegister(1, 0, new TextEncoder().encode(token)))
    : undefined;
  const client = new MOQtailClient({ url, setupOptions });
  let closed = false;
  client.on("sessionterminated", () => {
    closed = true;
    onDisconnected();
  });

  return {
    ready: client.init(),
    async subscribeMedia(name) {
      const result = await client.subscribe({
        fullTrackName: name,
        groupOrder: GroupOrder.Original,
        locationFilter: new LocationFilter(LocationFilterType.NextObject),
        forward: true,
        priority: 0,
      });
      if (result instanceof RequestError) {
        throw new Error("Relay rejected the media subscription");
      }
      return {
        stream: result.stream,
        unsubscribe: async () => {
          if (!closed) await client.unsubscribe(result.requestId);
        },
      };
    },
    publishMedia(names) {
      const namespace = names[0].namespace;
      const sources = new Map<string, ReturnType<typeof registerTrack>>();
      let stopping: Promise<void> | undefined;

      function registerTrack(name: FullTrackName) {
        let controller: ReadableStreamDefaultController<MoqtObject> | undefined;
        const stream = new ReadableStream<MoqtObject>({
          start(value) {
            controller = value;
          },
          cancel() {
            controller = undefined;
          },
        });
        const track = {
          fullTrackName: name,
          trackSource: { live: new LiveTrackSource(stream) },
          publisherPriority: 128,
        };
        client.addOrUpdateTrack(track);
        return {
          write: (object: MoqtObject) => controller?.enqueue(object),
          close() {
            controller?.close();
            controller = undefined;
          },
          remove: () => { if (!closed) client.removeTrack(track); },
        };
      }

      function addTrack(name: FullTrackName) {
        if (stopping) throw new Error("Media publication has stopped");
        if (!name.namespace.equals(namespace)) {
          throw new Error("Track must belong to the published namespace");
        }
        const key = name.toString();
        if (sources.has(key)) {
          throw new Error("Track is already registered");
        }
        const source = registerTrack(name);
        sources.set(key, source);
        return {
          stop() {
            if (sources.get(key) !== source) return;
            sources.delete(key);
            source.close();
            source.remove();
          },
        };
      }

      for (const name of names) addTrack(name);

      const ready = client.publishNamespace(namespace).then((result) => {
        if (result instanceof RequestError) {
          throw new Error("Relay rejected the media namespace");
        }
      });

      return {
        ready,
        addTrack,
        write: (object) => sources.get(object.fullTrackName.toString())?.write(object),
        stop() {
          if (!stopping) {
            for (const source of sources.values()) source.close();
            stopping = (async () => {
              try {
                await ready;
                if (!closed) await client.unpublishNamespace(namespace);
              } finally {
                for (const source of sources.values()) source.remove();
                sources.clear();
              }
            })();
          }
          return stopping;
        },
      };
    },
    async disconnect() {
      if (closed) return;
      closed = true;
      await client.disconnect();
    },
  };
}
