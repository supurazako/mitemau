import { GroupOrder, LocationFilter, LocationFilterType, RequestError } from "moqtail";
import type { FullTrackName, MoqtObject } from "moqtail";
import { LiveTrackSource, MOQtailClient } from "moqtail/client";

export const TEST_RELAY_URL = "https://relay-test.supurazako.com:4433";

export type VideoPublication = {
  ready: Promise<void>;
  write: (object: MoqtObject) => void;
  stop: () => Promise<void>;
};

export type RelaySession = {
  ready: Promise<void>;
  subscribeVideo: (name: FullTrackName) => Promise<{
    stream: ReadableStream<MoqtObject>;
    unsubscribe: () => Promise<void>;
  }>;
  publishVideo: (name: FullTrackName) => VideoPublication;
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
    async subscribeVideo(name) {
      const result = await client.subscribe({
        fullTrackName: name,
        groupOrder: GroupOrder.Original,
        locationFilter: new LocationFilter(LocationFilterType.NextObject),
        forward: true,
        priority: 0,
      });
      if (result instanceof RequestError) {
        throw new Error("Relay rejected the camera subscription");
      }
      return {
        stream: result.stream,
        unsubscribe: () => client.unsubscribe(result.requestId),
      };
    },
    publishVideo(name) {
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

      const ready = client.publishNamespace(name.namespace).then((result) => {
        if (result instanceof RequestError) {
          throw new Error("Relay rejected the camera namespace");
        }
      });

      let stopping: Promise<void> | undefined;
      return {
        ready,
        write: (object) => controller?.enqueue(object),
        stop() {
          if (!stopping) {
            controller?.close();
            controller = undefined;
            stopping = (async () => {
              try {
                await ready;
                await client.unpublishNamespace(name.namespace);
              } finally {
                client.removeTrack(track);
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
