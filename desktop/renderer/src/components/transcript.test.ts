import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';

const items = await readFile(new URL('./TranscriptItems.tsx', import.meta.url), 'utf8');
const markdown = await readFile(new URL('./Markdown.tsx', import.meta.url), 'utf8');
const transcript = await readFile(new URL('../core/transcript.ts', import.meta.url), 'utf8');
const stream = await readFile(new URL('./ChatStream.tsx', import.meta.url), 'utf8');
const stateSrc = await readFile(new URL('../core/state.ts', import.meta.url), 'utf8');
const styles = await readFile(new URL('../index.css', import.meta.url), 'utf8');

test('thought blocks render as a button with aria-expanded and default collapsed', () => {
  assert.match(
    items,
    /className="thought-toggle flex w-full[^"]*"[^>]*\n?\s*aria-expanded=\{item\.open\}/,
    'thought toggle must be a real full-width button exposing its collapsed state',
  );
  assert.match(
    items,
    /setTranscriptItemOpen\(item, !item\.open\)/,
    'thought toggle must persist the open state on the transcript item and re-render',
  );
  assert.match(items, /\{item\.open \? \(/, 'thought text must only render while expanded');
});

test('collapsed streaming thought shows a one-line ticker pinned to the newest text', () => {
  assert.match(
    items,
    /!item\.open && live \? thoughtPreview\(item\.text\)/,
    'the live preview must only appear while the thought is collapsed and streaming',
  );
  assert.match(
    items,
    /thought-preview block min-w-0 flex-1 overflow-hidden[^\"]*whitespace-nowrap/,
    'the live preview must occupy the remaining row width without wrapping',
  );
  assert.match(
    items,
    /const target = node\.scrollWidth - node\.clientWidth;/,
    'the ticker must scroll to keep the newest reasoning text visible',
  );
  assert.match(
    items,
    /node\.scrollLeft \+= Math\.ceil\(remaining \* 0\.35\);/,
    'the ticker must animate catch-up without restarting on every token',
  );
  assert.match(
    stream,
    /isSessionRunning\(appState\.activeSessionId\)/,
    'only the active run may project a live reasoning ticker',
  );
  assert.match(
    stream,
    /live=\{item\.key === liveThoughtKey\}/,
    'ChatStream must pass liveness to the current thought entry',
  );
});

test('tool cards render a button header with aria-expanded and never auto-expand on failure', () => {
  assert.match(
    items,
    /className="tool-head flex w-full[^"]*"[^>]*\n?\s*aria-expanded=\{item\.open\}/,
    'tool card header must be a real full-width button exposing its collapsed state',
  );
  assert.doesNotMatch(
    items,
    /status === 'failed'[^)]*open = true/,
    'tool card must not auto-expand when the status becomes failed',
  );
  assert.doesNotMatch(
    transcript,
    /status === 'failed'[^)]*open: true/,
    'tool projection must not auto-expand failed calls',
  );
});

test('tool_call and tool_call_update initialize tool disclosure state to collapsed', () => {
  assert.match(
    transcript,
    /case 'tool_call':[\s\S]*?open: false,[\s\S]*?contents: \[\],/,
    'tool_call must create tool items with open=false',
  );
  assert.match(
    transcript,
    /next = applyToolUpdate\(\s*\{ kind: 'tool',[\s\S]*?open: false, contents: \[\] \},/,
    'tool_call_update fallback must create tool items with open=false',
  );
  assert.match(
    stateSrc,
    /kind: 'tool'[\s\S]*?open: boolean/,
    'tool transcript item type must include an explicit open flag',
  );
});

test('older transcript pages preserve transcript order and the current scroll position', () => {
  assert.match(
    transcript,
    /export function applyTranscriptPage\(sessionId: string, updates: Record<string, unknown>\[\], prepend: boolean\)/,
    'chat must accept ACP-projected transcript pages without a separate local history format',
  );
  assert.match(
    transcript,
    /state\.transcript\.unshift\(\.\.\.resolved\)/,
    'older pages must be prepended before the already visible latest transcript',
  );
  assert.match(
    stream,
    /stream\.scrollTop = anchor\.top \+ stream\.scrollHeight - anchor\.height;/,
    'prepending a page must keep the reader at the same visible message',
  );
  assert.match(
    stream,
    /anchorRef\.current = \{ top: stream\.scrollTop, height: stream\.scrollHeight, count: appState\.transcript\.length \};/,
    'the stream must capture its scroll anchor before the older page is applied',
  );
});

test('disclosure state survives streaming updates because it lives on the transcript item', () => {
  assert.match(
    transcript,
    /Local disclosure state only\. Streaming ACP updates must not overwrite/,
    'the projection must document that streaming never overwrites disclosure choices',
  );
  assert.match(items, /key=\{item\.key\}|TranscriptEntry/, 'entries must be keyed for stable reconciliation');
  assert.match(stream, /key=\{item\.key\}/, 'stream rendering must key items by their canonical key');
});

test('transcript cards do not restart entrance animations when content-visibility reveals them', () => {
  assert.doesNotMatch(
    items,
    /animate-in fade-in/,
    'content-visibility:auto can start animate-in again on every scroll reveal; transcript cards must not use it',
  );
  assert.doesNotMatch(
    items,
    /duration-\d+/,
    'Tailwind duration-* also sets transition-duration, which makes the initial transition-property:all animate every color/border change',
  );
});

test('styles keep the button toggles keyboard-focusable', () => {
  assert.match(
    items,
    /thought-toggle[\s\S]*?focus-visible:outline-2/,
    'thought toggle should have a visible focus indicator',
  );
  assert.match(
    items,
    /tool-head[\s\S]*?focus-visible:outline-2/,
    'tool head should have a visible focus indicator',
  );
  assert.match(styles, /\.autohide-scrollbar \{/, 'component layer must retain the auto-hiding scrollbar system');
});

test('decision cards expose deadlines, allow/deny actions, and resolved notes', () => {
  assert.match(items, /deadlineText\(item\.deadline\)/, 'deadline must render through the shared formatter');
  assert.match(items, /useNow\(active\)/, 'deadline countdown must tick through the shared clock hook');
  assert.match(items, /resolvePermission\(item, option\.optionId\)/, 'permission options must resolve through ACP');
  assert.match(items, /cancelPermission\(item\)/, 'permission cards must support cancellation');
  assert.match(items, /resolveQuestion\(item, value\)/, 'question cards must submit custom answers');
  assert.match(items, /cancelQuestion\(item\)/, 'question cards must support skipping');
  assert.match(items, /t\('chat\.resolved', \{ v: item\.resolved \}\)/, 'resolved decisions must show their resolution');
});

test('agent text renders full markdown through react-markdown without HTML injection', () => {
  assert.match(markdown, /import ReactMarkdown/, 'agent text must render through react-markdown');
  assert.match(markdown, /remarkPlugins=\{\[remarkGfm\]\}/, 'tables/strikethrough/task lists must come from remark-gfm');
  assert.doesNotMatch(markdown, /rehype-raw/, 'raw HTML must never be rendered');
  assert.doesNotMatch(markdown, /dangerouslySetInnerHTML/, 'markdown must not inject raw HTML');
  assert.match(items, /<Markdown text=\{item\.text\} \/>/, 'agent messages must render through the shared Markdown component');
});

test('markdown links never navigate the app window', () => {
  assert.match(markdown, /event\.preventDefault\(\)/, 'link clicks must be intercepted');
  assert.match(markdown, /window\.open\(href, '_blank', 'noopener'\)/, 'links must route through the Electron window-open handler');
});

test('code blocks keep a copy affordance and bounded height', () => {
  assert.match(markdown, /navigator\.clipboard\.writeText/, 'code blocks must offer copy');
  assert.match(styles, /\.md-body \.md-pre \{[\s\S]*?max-height: 320px/, 'code blocks must stay bounded');
  assert.match(styles, /\.md-body code \{[\s\S]*?font-family: var\(--font-mono\)/, 'inline code must keep the mono chip styling');
});

test('diff rendering stays a pure projection with bounded output', () => {
  assert.match(transcript, /export function buildDiffLines\(oldText: string \| null, newText: string\): DiffLine\[\]/, 'diff must be computed as pure data');
  assert.match(transcript, /oldLines\.length > 1500 \|\| newLines\.length > 1500/, 'oversized files must degrade to a bounded view');
  assert.match(transcript, /budget = 800/, 'diff output must stay bounded');
  assert.match(items, /text\.length > 12000/, 'tool output must stay bounded');
});
