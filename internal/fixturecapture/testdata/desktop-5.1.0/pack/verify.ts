import { join } from "node:path";

type ManifestTrack = {
  fileName: string;
  format: string;
  bpm: number;
  seconds: number;
  title?: string;
  year?: number;
  comment?: string;
  label?: string;
  hasNoTags?: boolean;
};

type Manifest = { tracks: ManifestTrack[] };

type ProbedTags = Record<string, string | undefined>;

type ProbeResult = {
  format: { duration: string; tags?: ProbedTags };
  streams: { codec_name: string; tags?: ProbedTags }[];
};

const PACK_PATH = import.meta.dir;
const SAMPLE_RATE = 8000;
const ONSET_THRESHOLD = 6000;
const REFRACTORY_SAMPLES = SAMPLE_RATE / 10;
const MID3V2_COMMAND = ["uvx", "--quiet", "--from", "mutagen==1.47.0", "mid3v2"];

// ffprobe reports COMM and TXXX:comment alike, so only the ID3 frame listing proves a real comment.
const hasCommentFrame = (path: string, comment: string): boolean =>
  Bun.spawnSync([...MID3V2_COMMAND, "--list", path])
    .stdout.toString()
    .split("\n")
    .includes(`COMM==eng=${comment}`);

const measureBpm = (path: string): number => {
  const command = ["ffmpeg", "-loglevel", "error", "-i", path, "-ac", "1", "-ar", String(SAMPLE_RATE), "-f", "s16le", "-"];
  const bytes = Bun.spawnSync(command, { stderr: "pipe" }).stdout;
  const samples = new Int16Array(bytes.buffer, bytes.byteOffset, Math.floor(bytes.byteLength / 2));
  const onsets: number[] = [];
  samples.forEach((sample, index) => {
    const previous = onsets.at(-1) ?? -REFRACTORY_SAMPLES;
    if (Math.abs(sample) > ONSET_THRESHOLD && index - previous > REFRACTORY_SAMPLES) onsets.push(index);
  });
  const first = onsets.at(0) ?? 0;
  const last = onsets.at(-1) ?? 0;
  return (60 * SAMPLE_RATE * (onsets.length - 1)) / (last - first);
};

const probe = (path: string): ProbeResult => {
  const command = ["ffprobe", "-v", "error", "-show_format", "-show_streams", "-of", "json", path];
  return JSON.parse(Bun.spawnSync(command).stdout.toString());
};

const manifest: Manifest = await Bun.file(join(PACK_PATH, "manifest.json")).json();
const problems: string[] = [];

for (const track of manifest.tracks) {
  const path = join(PACK_PATH, "tracks", track.fileName);
  const result = probe(path);
  const tags: ProbedTags = { ...result.format.tags, ...result.streams[0]?.tags };
  const lowered = Object.fromEntries(Object.entries(tags).map(([key, value]) => [key.toLowerCase(), value]));
  const duration = Number(result.format.duration);
  const bpm = measureBpm(path);
  if (result.streams[0]?.codec_name !== track.format) problems.push(`${track.fileName}: codec`);
  if (Math.abs(duration - track.seconds) > 0.1) problems.push(`${track.fileName}: duration ${duration}`);
  if (Math.abs(bpm - track.bpm) > 0.5) problems.push(`${track.fileName}: measured ${bpm.toFixed(2)} BPM`);
  if (lowered.title !== track.title) problems.push(`${track.fileName}: title ${lowered.title}`);
  if (track.year && !lowered.date?.startsWith(String(track.year))) problems.push(`${track.fileName}: date ${lowered.date}`);
  if (lowered.comment !== track.comment) problems.push(`${track.fileName}: comment ${lowered.comment}`);
  if (track.comment && !hasCommentFrame(path, track.comment)) problems.push(`${track.fileName}: no COMM frame`);
  if (track.label && lowered.publisher !== track.label) problems.push(`${track.fileName}: label ${lowered.publisher}`);
  if (track.hasNoTags && lowered.artist) problems.push(`${track.fileName}: unexpected artist`);
  console.log(track.fileName.padEnd(26), duration.toFixed(2).padStart(6), bpm.toFixed(2).padStart(7), lowered.title ?? "(none)");
}

console.log(problems.length === 0 ? "All tracks match the manifest" : problems.join("\n"));
process.exitCode = problems.length === 0 ? 0 : 1;
