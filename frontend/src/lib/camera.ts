export async function startCamera(): Promise<MediaStream> {
  return navigator.mediaDevices.getUserMedia({
    video: true,
    audio: false,
  });
}

export function stopCamera(stream: MediaStream): void {
  for (const track of stream.getTracks()) {
    track.stop();
  }
}
