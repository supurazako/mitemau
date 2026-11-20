import { GroupOrder, LocationFilter, LocationFilterType, RequestError } from "moqtail";
import type { FullTrackName, MoqtObject } from "moqtail";
import { LiveTrackSource, MOQtailClient } from "moqtail/client";

export const TEST_RELAY_URL = "https://relay-test.supurazako.com:4433";

export type MediaPublication = {
  ready: Promise<void>;
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
): RelaySession {
  const client = new MOQtailClient({ url });
  client.on("sessionterminated", onDisconnected);

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
        unsubscribe: () => client.unsubscribe(result.requestId),
      };
    },
    publishMedia(names) {
      const sources = new Map(names.map((name) => {
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
        return [name, {
          track,
          write: (object: MoqtObject) => controller?.enqueue(object),
          close() {
            controller?.close();
            controller = undefined;
          },
        }];
      }));

      const namespace = names[0].namespace;
      const ready = client.publishNamespace(namespace).then((result) => {
        if (result instanceof RequestError) {
          throw new Error("Relay rejected the media namespace");
        }
      });

      let stopping: Promise<void> | undefined;
      return {
        ready,
        write: (object) => sources.get(object.fullTrackName)?.write(object),
        stop() {
          if (!stopping) {
            for (const source of sources.values()) source.close();
            stopping = (async () => {
              try {
                await ready;
                await client.unpublishNamespace(namespace);
              } finally {
                for (const source of sources.values()) client.removeTrack(source.track);
              }
            })();
          }
          return stopping;
        },
      };
    },
    disconnect: () => client.disconnect(),
  };
}
