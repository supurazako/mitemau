import { LOCProperties, Timestamp } from "moqtail";
import type { MoqtObject } from "moqtail";
import type { PlaybackTimeline } from "./mediaTiming";
import { MICROPHONE_CODEC } from "./audioEncoding";

export function createAudioPlayback(
  context: AudioContext,
  timeline: PlaybackTimeline,
  onError: (error: DOMException) => void,
) {
  const sources = new Set<AudioBufferSourceNode>();
  let finishDrain: (() => void) | undefined;

  const decoder = new AudioDecoder({
    output(data) {
      try {
        const buffer = context.createBuffer(
          data.numberOfChannels,
          data.numberOfFrames,
          data.sampleRate,
        );
        for (let channel = 0; channel < data.numberOfChannels; channel++) {
          data.copyTo(buffer.getChannelData(channel), {
            planeIndex: channel,
            format: "f32-planar",
          });
        }
        const source = context.createBufferSource();
        source.buffer = buffer;
        source.connect(context.destination);
        source.onended = () => {
          source.disconnect();
          sources.delete(source);
          if (sources.size === 0) finishDrain?.();
        };
        sources.add(source);
        source.start(timeline.scheduleAudio(data.timestamp));
      } finally {
        data.close();
      }
    },
    error: onError,
  });

  try {
    decoder.configure(MICROPHONE_CODEC);
  } catch (error) {
    decoder.close();
    throw error;
  }

  return {
    async play(stream: ReadableStream<MoqtObject>) {
      const reader = stream.getReader();
      try {
        while (decoder.state !== "closed") {
          const { value, done } = await reader.read();
          if (done) break;
          if (decoder.state !== "configured") return;
          if (!value.payload || !value.properties) continue;
          const timestamp = LOCProperties.fromKeyValuePairs(value.properties)
            .find((property) => property instanceof Timestamp);
          if (!timestamp) continue;
          decoder.decode(new EncodedAudioChunk({
            type: "key",
            timestamp: Number(timestamp.timestamp),
            data: new Uint8Array(value.payload),
          }));
        }
        if (decoder.state === "configured") await decoder.flush();
        if (sources.size > 0) {
          await new Promise<void>((resolve) => { finishDrain = resolve; });
        }
      } finally {
        reader.releaseLock();
      }
    },
    stop() {
      if (decoder.state !== "closed") decoder.close();
      for (const source of sources) {
        source.stop();
        source.disconnect();
      }
      sources.clear();
      finishDrain?.();
    },
  };
}
