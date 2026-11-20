export async function startCamera(withMicrophone = false): Promise<MediaStream> {
  return navigator.mediaDevices.getUserMedia({
    video: true,
    audio: withMicrophone ? { sampleRate: 48000, channelCount: 1 } : false,
  });
}

export function stopCamera(stream: MediaStream): void {
  for (const track of stream.getTracks()) {
    track.stop();
  }
}
