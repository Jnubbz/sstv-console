// Runs in the browser's audio thread: hands each block of the radio's audio to the page.
class Tap extends AudioWorkletProcessor {
  process(inputs) {
    const ch = inputs[0] && inputs[0][0];
    if (ch) this.port.postMessage(ch.slice(0));
    return true;
  }
}
registerProcessor("sstv-tap", Tap);
