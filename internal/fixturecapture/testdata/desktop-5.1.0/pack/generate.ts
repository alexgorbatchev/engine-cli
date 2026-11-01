import { mkdir, rm } from "node:fs/promises";
import { join } from "node:path";

type Format = "mp3" | "flac";

type TrackSpec = {
  number: number;
  slug: string;
  format?: Format;
  title?: string;
  artist?: string;
  album?: string;
  genre?: string;
  comment?: string;
  label?: string;
  bpm?: number;
  seconds?: number;
  year?: number;
  stars?: number;
  camelot?: string;
  keyName?: string;
  keyIndex?: number;
  stayUnanalyzed?: boolean;
  generateStems?: boolean;
  clearComment?: boolean;
  hasNoTags?: boolean;
};

type Track = Required<Pick<TrackSpec, "number" | "format" | "bpm" | "seconds">> & TrackSpec & { fileName: string };

type Rule = { filter: string; condition: string; value?: string };

type Smartlist = {
  name: string;
  rules: Rule[];
  matchAll?: boolean;
  placement?: string;
  isUniformResultExpected?: boolean;
  predictionOverride?: string;
  note?: string;
};

const PACK_PATH = import.meta.dir;
const TRACKS_PATH = join(PACK_PATH, "tracks");
const DEFAULT_BPM = 128;
const DEFAULT_SECONDS = 180;
const SAMPLE_RATE = 44100;
const MID3V2_COMMAND = ["uvx", "--quiet", "--from", "mutagen==1.47.0", "mid3v2"];

const TRACK_SPECS: TrackSpec[] = [
  { number: 1, slug: "deep-house", title: "Deep House" },
  { number: 2, slug: "house-party", title: "House Party" },
  { number: 3, slug: "warehouse", title: "Warehouse" },
  { number: 4, slug: "house-upper", title: "HOUSE" },
  { number: 5, slug: "techno", title: "Techno", generateStems: true },
  { number: 6, slug: "techno-upper", title: "TECHNO" },
  { number: 7, slug: "trance", title: "Trance" },
  { number: 8, slug: "special", title: `Mike's "100%_a\\b" Mix` },
  { number: 9, slug: "special-decoy", title: `Mike's "100 percent-a\\b" Mix` },
  { number: 10, slug: "unicode", title: "Café 東京" },
  { number: 11, slug: "unicode-decoy", title: "Cafe Tokyo" },
  { number: 12, slug: "percent", title: "100%" },
  { number: 13, slug: "percent-decoy", title: "100X" },
  { number: 14, slug: "field-artist", title: "Field Artist", artist: "Zebra" },
  { number: 15, slug: "field-album", title: "Field Album", album: "Yonder" },
  { number: 16, slug: "field-genre", title: "Field Genre", genre: "Xylo" },
  { number: 17, slug: "field-comment", title: "Field Comment", comment: "Wombat" },
  { number: 18, slug: "field-label", title: "Field Label", label: "Vortex" },
  { number: 19, slug: "field-none", title: "Field None" },
  { number: 20, slug: "comment-cleared", title: "Comment Cleared", comment: "clear me", clearComment: true },
  {
    number: 21,
    slug: "number-one",
    title: "Number One",
    bpm: 119,
    year: 1999,
    stars: 1,
    camelot: "8A",
    keyName: "A minor",
    keyIndex: 1,
  },
  {
    number: 22,
    slug: "number-two",
    title: "Number Two",
    bpm: 120,
    year: 2000,
    stars: 2,
    camelot: "8B",
    keyName: "C major",
    keyIndex: 0,
  },
  {
    number: 23,
    slug: "number-three",
    title: "Number Three",
    bpm: 121,
    year: 2010,
    stars: 3,
    camelot: "7A",
    keyName: "D minor",
    keyIndex: 23,
  },
  {
    number: 24,
    slug: "number-four",
    title: "Number Four",
    bpm: 125,
    year: 2020,
    stars: 4,
    camelot: "9A",
    keyName: "E minor",
    keyIndex: 3,
  },
  {
    number: 25,
    slug: "number-five",
    title: "Number Five",
    bpm: 126,
    year: 2021,
    stars: 5,
    camelot: "2B",
    keyName: "F♯ major",
    keyIndex: 12,
  },
  { number: 26, slug: "length-59", title: "Length 59", format: "flac", seconds: 59 },
  { number: 27, slug: "length-60", title: "Length 60", format: "flac", seconds: 60 },
  { number: 28, slug: "length-61", title: "Length 61", format: "flac", seconds: 61 },
  { number: 29, slug: "unanalyzed-one", title: "Unanalyzed One", stayUnanalyzed: true },
  { number: 30, slug: "unanalyzed-two", title: "Unanalyzed Two", stayUnanalyzed: true },
  { number: 31, slug: "no-tags", hasNoTags: true },
];

const titleRule = (condition: string, value: string): Rule => ({ filter: "Title", condition, value });
const bpmRule = (condition: string, value: string): Rule => ({ filter: "BPM", condition, value });
const lengthRule = (condition: string): Rule => ({ filter: "Length", condition, value: "01:00" });
const ratingRule = (condition: string): Rule => ({ filter: "Rating", condition, value: "3 stars" });
const flacRule: Rule = { filter: "File Type", condition: "is equal to", value: "flac" };
const numberTrackRule = titleRule("begins with", "Number");

const SMARTLISTS: Smartlist[] = [
  { name: "T1", rules: [titleRule("contains", "house")] },
  { name: "T2", rules: [titleRule("does not contain", "house")] },
  { name: "T3", rules: [titleRule("is equal to", "Techno")] },
  { name: "T4", rules: [titleRule("is not equal to", "Techno")] },
  { name: "T5", rules: [titleRule("begins with", "House")] },
  { name: "T6", rules: [titleRule("ends with", "house")] },
  {
    name: "T7",
    rules: [{ filter: "Comment", condition: "is empty", value: "True" }],
    note: "Track 20 is the empty-string case: in or out here shows how Desktop stores a cleared comment.",
  },
  { name: "T8", rules: [{ filter: "Comment", condition: "is empty", value: "False" }] },
  { name: "T9", rules: [titleRule("contains", `e's "100%_a\\b"`)], note: "Type the value exactly, quotes included." },
  { name: "T10", rules: [titleRule("contains", "Café 東京")] },
  {
    name: "T11",
    rules: [titleRule("is equal to", "100%")],
    predictionOverride: "error, 0 or 12",
    note: "An error or 0 tracks fits the suspected defect. Screenshot any error.",
  },
  {
    name: "C1",
    rules: [
      { filter: "Artist", condition: "contains", value: "Zebra" },
      { filter: "Album", condition: "contains", value: "Yonder" },
      { filter: "Genre", condition: "contains", value: "Xylo" },
      { filter: "Comment", condition: "contains", value: "Wombat" },
      { filter: "Label", condition: "contains", value: "Vortex" },
    ],
    matchAll: false,
  },
  { name: "F1", rules: [{ filter: "File Type", condition: "is equal to", value: "mp3" }] },
  { name: "B1", rules: [numberTrackRule, bpmRule("is greater than", "120")], matchAll: true },
  { name: "B2", rules: [numberTrackRule, bpmRule("is less than", "120")], matchAll: true },
  { name: "B3", rules: [numberTrackRule, bpmRule("is equal to", "120")], matchAll: true },
  { name: "B4", rules: [numberTrackRule, bpmRule("is in the range of", "120 to 125")], matchAll: true },
  { name: "Y1", rules: [{ filter: "Year", condition: "is in the range of", value: "2000 to 2020" }] },
  { name: "L1", rules: [lengthRule("is greater than")] },
  { name: "L2", rules: [lengthRule("is less than")] },
  { name: "L3", rules: [lengthRule("is equal to")] },
  { name: "R1", rules: [ratingRule("is greater than")] },
  { name: "R2", rules: [ratingRule("is less than")] },
  { name: "R3", rules: [ratingRule("is equal to")] },
  {
    name: "D1",
    rules: [{ filter: "Date Added", condition: "is equal to", value: "import day" }],
    isUniformResultExpected: true,
  },
  {
    name: "D2",
    rules: [{ filter: "Date Added", condition: "before", value: "import day" }],
    isUniformResultExpected: true,
  },
  {
    name: "D3",
    rules: [{ filter: "Date Added", condition: "after", value: "day before import" }],
    isUniformResultExpected: true,
  },
  { name: "K1", rules: [numberTrackRule, { filter: "Key", condition: "is equal to", value: "8A" }], matchAll: true },
  { name: "K2", rules: [numberTrackRule, { filter: "Key", condition: "compatible", value: "8A" }], matchAll: true },
  { name: "G1", rules: [{ filter: "Grid", condition: "exists", value: "True" }] },
  { name: "G2", rules: [{ filter: "Grid", condition: "exists", value: "False" }] },
  { name: "S1", rules: [{ filter: "Stems", condition: "exists", value: "True" }] },
  {
    name: "S2",
    rules: [{ filter: "Stems", condition: "exists", value: "False" }],
    predictionOverride: "unknown",
    note: "Tracks with no stems status may be left out.",
  },
  {
    name: "M1",
    rules: [bpmRule("is greater than", "120"), { filter: "Year", condition: "is less than", value: "2021" }],
    matchAll: true,
  },
  {
    name: "M2",
    rules: [
      titleRule("begins with", "Number"),
      titleRule("ends with", "Three"),
      { filter: "Artist", condition: "contains", value: "Fixture" },
      { filter: "File Type", condition: "is equal to", value: "mp3" },
      bpmRule("is in the range of", "120 to 125"),
      { filter: "Year", condition: "is equal to", value: "2010" },
      { filter: "Rating", condition: "is equal to", value: "3 stars" },
      { filter: "Key", condition: "is equal to", value: "7A" },
    ],
    matchAll: true,
  },
  { name: "P1", rules: [flacRule], placement: "Library root" },
  { name: "P2", rules: [flacRule], placement: "Inside Beta > Child" },
  { name: "P3", rules: [flacRule], placement: "Library root, between Alpha and Beta" },
];

const toTrack = (spec: TrackSpec): Track => {
  const format = spec.format ?? "mp3";
  const fileName = `${String(spec.number).padStart(2, "0")}-${spec.slug}.${format}`;
  return { ...spec, format, bpm: spec.bpm ?? DEFAULT_BPM, seconds: spec.seconds ?? DEFAULT_SECONDS, fileName };
};

const predictedText = (track: Track, filter: string): string | null => {
  if (filter === "Title") return track.title ?? track.fileName.replace(/\.[a-z0-9]+$/, "");
  if (track.hasNoTags) return null;
  if (filter === "Artist") return track.artist ?? "Fixture";
  if (filter === "Album") return track.album ?? "Pack";
  if (filter === "Genre") return track.genre ?? "Test";
  if (filter === "Label") return track.label ?? null;
  if (filter === "Comment") return track.clearComment ? "" : (track.comment ?? null);
  throw new Error(`Not a text filter: ${filter}`);
};

const parseMinutes = (value: string): number => {
  const [minutes = "0", seconds = "0"] = value.split(":");
  return Number(minutes) * 60 + Number(seconds);
};

const predictedNumber = (track: Track, filter: string): number | null => {
  if (filter === "BPM") return track.stayUnanalyzed ? null : track.bpm;
  if (filter === "Year") return track.year ?? null;
  if (filter === "Length") return track.seconds;
  if (filter === "Rating") return track.stars ?? 0;
  throw new Error(`Not a numeric filter: ${filter}`);
};

const parseRuleNumber = (filter: string, value: string): number => {
  if (filter === "Length") return parseMinutes(value);
  return Number.parseInt(value, 10);
};

const isCompatibleKey = (track: Track, value: string): boolean => {
  const target = TRACK_SPECS.find((spec) => spec.camelot === value)?.keyIndex;
  if (target === undefined || track.keyIndex === undefined) return false;
  const relative = target % 2 === 1 ? target - 1 : target + 1;
  return [target, relative, target - 2, target + 2].map((key) => (key + 24) % 24).includes(track.keyIndex);
};

const matchesTextRule = (track: Track, rule: Rule, value: string): boolean => {
  const text = predictedText(track, rule.filter);
  if (rule.condition === "is empty") return (text === null) === (value === "True");
  if (text === null) return false;
  const haystack = text.toLowerCase();
  const needle = value.toLowerCase();
  if (rule.condition === "contains") return haystack.includes(needle);
  if (rule.condition === "does not contain") return !haystack.includes(needle);
  if (rule.condition === "is equal to") return text === value;
  if (rule.condition === "is not equal to") return text !== value;
  if (rule.condition === "begins with") return haystack.startsWith(needle);
  if (rule.condition === "ends with") return haystack.endsWith(needle);
  throw new Error(`Not a text condition: ${rule.condition}`);
};

const matchesNumericRule = (track: Track, rule: Rule, value: string): boolean => {
  const number = predictedNumber(track, rule.filter);
  if (number === null) return false;
  if (rule.condition === "is in the range of") {
    const [low = "", high = ""] = value.split(" to ");
    return number >= Number(low) && number <= Number(high);
  }
  const target = parseRuleNumber(rule.filter, value);
  if (rule.condition === "is greater than") return number > target;
  if (rule.condition === "is less than") return number < target;
  if (rule.condition === "is equal to") return number === target;
  throw new Error(`Not a numeric condition: ${rule.condition}`);
};

const matchesRule = (track: Track, rule: Rule): boolean => {
  const value = rule.value ?? "";
  if (rule.filter === "File Type") return track.format === value;
  if (rule.filter === "Grid") return !track.stayUnanalyzed === (value === "True");
  if (rule.filter === "Stems") return Boolean(track.generateStems) === (value === "True");
  if (rule.filter === "Key") {
    return rule.condition === "compatible" ? isCompatibleKey(track, value) : track.camelot === value;
  }
  if (rule.filter === "Date Added") {
    const importDay = 0;
    const target = value === "import day" ? importDay : importDay - 1;
    if (rule.condition === "is equal to") return importDay === target;
    return rule.condition === "before" ? importDay < target : importDay > target;
  }
  if (["BPM", "Year", "Length", "Rating"].includes(rule.filter)) return matchesNumericRule(track, rule, value);
  return matchesTextRule(track, rule, value);
};

const predictMembers = (tracks: Track[], list: Smartlist): Track[] => {
  const isMatchAll = list.matchAll ?? true;
  return tracks.filter((track) =>
    isMatchAll ? list.rules.every((rule) => matchesRule(track, rule)) : list.rules.some((rule) => matchesRule(track, rule)),
  );
};

const describeMembers = (tracks: Track[], list: Smartlist): string => {
  if (list.predictionOverride) return list.predictionOverride;
  const members = predictMembers(tracks, list);
  if (members.length === tracks.length) return "all 31";
  if (members.length === 0) return "none";
  const numbers = members.map((track) => track.number);
  if (numbers.length <= tracks.length / 2) return `${numbers.length}: ${numbers.join(", ")}`;
  const excluded = tracks.filter((track) => !numbers.includes(track.number)).map((track) => track.number);
  return `${numbers.length}: all except ${excluded.join(", ")}`;
};

const assertListsDiscriminate = (tracks: Track[]): void => {
  if (SMARTLISTS.length !== 38) throw new Error(`Expected 38 smartlists, got ${SMARTLISTS.length}`);
  for (const list of SMARTLISTS) {
    if (list.rules.length > 8) throw new Error(`${list.name} exceeds Desktop's 8 rules`);
    const count = predictMembers(tracks, list).length;
    const isUniform = count === 0 || count === tracks.length;
    if (isUniform && !list.isUniformResultExpected) throw new Error(`${list.name} does not discriminate: ${count}`);
  }
};

const buildMetadataArguments = (track: Track): string[] => {
  if (track.hasNoTags) return ["-map_metadata", "-1"];
  const tags: Record<string, string | undefined> = {
    title: track.title,
    artist: track.artist ?? "Fixture",
    album: track.album ?? "Pack",
    genre: track.genre ?? "Test",
    date: track.year?.toString(),
    publisher: track.label,
  };
  return Object.entries(tags).flatMap(([key, value]) => (value === undefined ? [] : ["-metadata", `${key}=${value}`]));
};

const buildSource = (track: Track): string => {
  const beat = (60 / track.bpm).toFixed(9);
  const tone = 50 + track.number;
  const kick = `0.8*sin(2*PI*${tone}*t)*exp(-30*mod(t,${beat}))`;
  const transient = `0.4*(random(0)*2-1)*exp(-200*mod(t,${beat}))`;
  return `aevalsrc='${kick}+${transient}':s=${SAMPLE_RATE}:d=${track.seconds}`;
};

const buildFfmpegArguments = (track: Track): string[] => {
  const codec =
    track.format === "mp3"
      ? ["-c:a", "libmp3lame", "-b:a", "192k", "-id3v2_version", "3", "-write_id3v1", "0"]
      : ["-c:a", "flac"];
  const outputPath = join(TRACKS_PATH, track.fileName);
  return [
    "ffmpeg",
    "-hide_banner",
    "-loglevel",
    "error",
    "-y",
    "-f",
    "lavfi",
    "-i",
    buildSource(track),
    "-ac",
    "2",
    ...buildMetadataArguments(track),
    "-bitexact",
    ...codec,
    outputPath,
  ];
};

const renderTrackRow = (track: Track): string => {
  const title = (track.title ?? "(no tags; Desktop picks the name)").replaceAll("|", "\\|");
  const year = track.year ?? "";
  return `| ${track.number} | \`${track.fileName}\` | \`${title}\` | ${track.format} | ${track.seconds} | ${track.bpm} | ${year} |`;
};

const renderHandEdit = (track: Track): string =>
  `   | ${track.number} | ${track.title} | ${track.stars} | ${track.camelot} | ${track.keyName} |`;

const renderRule = (rule: Rule): string => `${rule.filter} ${rule.condition} \`${rule.value ?? ""}\``;

const renderSmartlistRow = (tracks: Track[], list: Smartlist): string => {
  const match = list.rules.length === 1 ? "" : list.matchAll ? "Match all ON: " : "Match all OFF: ";
  const rules = list.rules.map(renderRule).join("; ").replaceAll("|", "\\|");
  return `| ${list.name} | ${match}${rules} | ${describeMembers(tracks, list)} | ${list.note ?? ""} |`;
};

const buildChecklist = (template: string, tracks: Track[]): string =>
  template
    .replaceAll("{{PACK_PATH}}", PACK_PATH)
    .replace("{{TRACK_ROWS}}", () => tracks.map(renderTrackRow).join("\n"))
    .replace("{{HAND_EDIT_ROWS}}", () =>
      tracks
        .filter((track) => track.stars !== undefined)
        .map(renderHandEdit)
        .join("\n"),
    )
    .replace("{{SMARTLIST_ROWS}}", () => SMARTLISTS.map((list) => renderSmartlistRow(tracks, list)).join("\n"));

const main = async (): Promise<void> => {
  const tracks = TRACK_SPECS.map(toTrack);
  assertListsDiscriminate(tracks);
  await rm(TRACKS_PATH, { recursive: true, force: true });
  await mkdir(TRACKS_PATH, { recursive: true });
  await mkdir(join(PACK_PATH, "returned"), { recursive: true });
  for (const track of tracks) {
    const result = Bun.spawnSync(buildFfmpegArguments(track), { stderr: "pipe" });
    if (result.exitCode !== 0) throw new Error(`${track.fileName}: ${result.stderr.toString()}`);
    // ffmpeg stores an mp3 comment as TXXX:comment, so the COMM frame has to come from mutagen.
    if (track.comment === undefined) continue;
    const commentCommand = [...MID3V2_COMMAND, `--comment=${track.comment}`, join(TRACKS_PATH, track.fileName)];
    const commentResult = Bun.spawnSync(commentCommand, { stderr: "pipe" });
    if (commentResult.exitCode !== 0) throw new Error(`${track.fileName}: ${commentResult.stderr.toString()}`);
  }
  const manifest = {
    tracks,
    smartlists: SMARTLISTS.map((list) => ({ ...list, predicted: describeMembers(tracks, list) })),
  };
  await Bun.write(join(PACK_PATH, "manifest.json"), `${JSON.stringify(manifest, null, 2)}\n`);
  const template = await Bun.file(join(PACK_PATH, "CHECKLIST.template.md")).text();
  await Bun.write(join(PACK_PATH, "CHECKLIST.md"), buildChecklist(template, tracks));
  console.log(`Generated ${tracks.length} tracks and ${SMARTLISTS.length} smartlist rows`);
};

await main();
