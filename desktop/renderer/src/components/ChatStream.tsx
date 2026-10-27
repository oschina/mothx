// 聊天流:滚动语义与旧 renderer 一致——距底 <80px 时跟随流式输出;滚动到
// 顶部或点击「加载更早消息」分页拉取历史,并保持当前阅读位置不跳动。
//
// The live transcript can grow without bound during a long-running task. Keep
// all canonical items in state (re-opening the task still has every message),
// but render a tail window so the DOM/Markdown/React reconciliation cost stays
// bounded. Scrolling up freezes the window; "load earlier" expands it.

import { useEffect, useLayoutEffect, useRef, useState } from 'react';

import { chatScrollEvents } from '@/core/bus';
import { t } from '@/core/i18n';
import { isSessionRunning } from '@/core/state';
import { loadOlderTranscript } from '@/core/sessions';
import { useAppState } from '@/hooks/useAppState';
import { TranscriptEntry } from '@/components/TranscriptItems';

export const TRANSCRIPT_RENDER_WINDOW = 300;

interface ScrollAnchor {
  top: number;
  height: number;
  count: number;
}

export function ChatStream() {
  const appState = useAppState();
  const streamRef = useRef<HTMLDivElement>(null);
  const autoScrollRef = useRef(true);
  const anchorRef = useRef<ScrollAnchor | null>(null);
  const loadingRef = useRef(false);
  const followingRef = useRef(true);
  const sessionIdRef = useRef(appState.transcriptSessionId);
  const transcriptLengthRef = useRef(appState.transcript.length);
  transcriptLengthRef.current = appState.transcript.length;

  const [renderLimit, setRenderLimit] = useState(TRANSCRIPT_RENDER_WINDOW);
  const [following, setFollowing] = useState(true);
  // When the reader scrolls away from the bottom, the rendered tail stays
  // frozen at this item count. New streamed items accumulate in state below
  // the window without changing its height.
  const [frozenEnd, setFrozenEnd] = useState(0);

  useEffect(() => {
    if (sessionIdRef.current === appState.transcriptSessionId) return;
    sessionIdRef.current = appState.transcriptSessionId;
    followingRef.current = true;
    setFollowing(true);
    setRenderLimit(TRANSCRIPT_RENDER_WINDOW);
    setFrozenEnd(appState.transcript.length);
  }, [appState.transcriptSessionId]);

  const setFollow = (next: boolean) => {
    if (followingRef.current === next) return;
    followingRef.current = next;
    setFollowing(next);
  };

  const end = following ? appState.transcript.length : Math.min(frozenEnd, appState.transcript.length);
  const start = Math.max(0, end - renderLimit);
  const visibleItems = start > 0 || end < appState.transcript.length
    ? appState.transcript.slice(start, end)
    : appState.transcript;

  // Only the thought that is currently producing tokens gets the inline live
  // preview; completed thought blocks stay as compact as before.
  let liveThoughtKey: string | null = null;
  if (isSessionRunning(appState.activeSessionId)) {
    for (let index = visibleItems.length - 1; index >= 0; index -= 1) {
      const item = visibleItems[index];
      if (item.kind === 'status') continue;
      liveThoughtKey = item.kind === 'thought' ? item.key : null;
      break;
    }
  }

  useEffect(() => {
    let frame = 0;
    const follow = () => {
      frame = 0;
      const stream = streamRef.current;
      if (stream && autoScrollRef.current) stream.scrollTop = stream.scrollHeight;
    };
    return chatScrollEvents.on(({ force }) => {
      if (force) {
        autoScrollRef.current = true;
        followingRef.current = true;
        setFollowing(true);
        setFrozenEnd(transcriptLengthRef.current);
      }
      // Coalesce the follow-up scroll with the frame that will paint the
      // streamed chunk instead of forcing one layout per token.
      if (autoScrollRef.current && frame === 0) frame = requestAnimationFrame(follow);
    });
  }, []);

  // 每次转录提交后:优先应用分页锚点(保持阅读位置),否则按 autoScroll 跟随。
  useLayoutEffect(() => {
    const stream = streamRef.current;
    if (!stream) return;
    const anchor = anchorRef.current;
    if (anchor) {
      anchorRef.current = null;
      // ACP paging grows the transcript; expanding the local window does not.
      // Both change scrollHeight by exactly the amount inserted above, so the
      // same correction keeps the reader's content in place. A failed load has
      // no delta and falls through to normal follow behavior.
      if (stream.scrollHeight - anchor.height > 0) {
        stream.scrollTop = anchor.top + stream.scrollHeight - anchor.height;
        return;
      }
    }
    if (autoScrollRef.current) stream.scrollTop = stream.scrollHeight;
  });

  const loadOlder = async () => {
    const stream = streamRef.current;
    if (!stream || loadingRef.current) return;
    if (!appState.transcriptNextCursor || appState.transcriptLoading) return;
    loadingRef.current = true;
    anchorRef.current = { top: stream.scrollTop, height: stream.scrollHeight, count: appState.transcript.length };
    try {
      await loadOlderTranscript();
    } finally {
      loadingRef.current = false;
    }
  };

  const showEarlier = () => {
    const stream = streamRef.current;
    if (!stream) return;
    anchorRef.current = { top: stream.scrollTop, height: stream.scrollHeight, count: appState.transcript.length };
    setRenderLimit((limit) => limit + TRANSCRIPT_RENDER_WINDOW);
  };

  return (
    <div
      ref={streamRef}
      className="min-h-0 flex-1 overflow-y-auto pt-5 pb-3"
      onScroll={() => {
        const stream = streamRef.current;
        if (!stream) return;
        const atBottom = stream.scrollHeight - stream.scrollTop - stream.clientHeight < 80;
        autoScrollRef.current = atBottom;
        if (atBottom) {
          setFollow(true);
        } else if (followingRef.current) {
          // Leaving the bottom freezes the current tail window, so new items
          // arriving during a long run cannot move the reader's viewport.
          setFrozenEnd(appState.transcript.length);
          setFollow(false);
        }
        if (
          stream.scrollTop < 80 &&
          start === 0 &&
          appState.transcriptNextCursor &&
          !appState.transcriptLoading
        ) {
          void loadOlder();
        }
      }}
    >
      <div className="flex justify-center">
        {start > 0 ? (
          <button
            type="button"
            className="my-2 rounded-lg border border-borderstrong bg-card px-3 py-1.5 text-[11.5px] hover:border-primary hover:text-primary disabled:opacity-55"
            onClick={showEarlier}
          >
            {t('chat.loadEarlier')}
          </button>
        ) : appState.transcriptNextCursor ? (
          <button
            type="button"
            className="my-2 rounded-lg border border-borderstrong bg-card px-3 py-1.5 text-[11.5px] hover:border-primary hover:text-primary disabled:opacity-55"
            disabled={appState.transcriptLoading}
            onClick={() => void loadOlder()}
          >
            {appState.transcriptLoading ? '…' : t('chat.loadEarlier')}
          </button>
        ) : null}
      </div>
      <div className="mx-auto flex w-[min(760px,94%)] flex-col gap-4">
        {visibleItems.map((item) => (
          <div key={item.key} className="transcript-entry">
            <TranscriptEntry item={item} live={item.key === liveThoughtKey} />
          </div>
        ))}
      </div>
    </div>
  );
}
