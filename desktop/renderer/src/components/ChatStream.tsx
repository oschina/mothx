// 聊天流:滚动语义与旧 renderer 一致——距底 <80px 时跟随流式输出;滚动到
// 顶部或点击「加载更早消息」分页拉取历史,并保持当前阅读位置不跳动。

import { useEffect, useLayoutEffect, useRef } from 'react';

import { chatScrollEvents } from '@/core/bus';
import { t } from '@/core/i18n';
import { isSessionRunning } from '@/core/state';
import { loadOlderTranscript } from '@/core/sessions';
import { useAppState } from '@/hooks/useAppState';
import { TranscriptEntry } from '@/components/TranscriptItems';

export function ChatStream() {
  const appState = useAppState();
  const streamRef = useRef<HTMLDivElement>(null);
  const autoScrollRef = useRef(true);
  const anchorRef = useRef<{ top: number; height: number; count: number } | null>(null);
  const loadingRef = useRef(false);
  // Only the thought that is currently producing tokens gets the inline live
  // preview; completed thought blocks stay as compact as before.
  let liveThoughtKey: string | null = null;
  if (isSessionRunning(appState.activeSessionId)) {
    for (let index = appState.transcript.length - 1; index >= 0; index -= 1) {
      const item = appState.transcript[index];
      if (item.kind === 'status') continue;
      liveThoughtKey = item.kind === 'thought' ? item.key : null;
      break;
    }
  }

  useEffect(() => {
    return chatScrollEvents.on(({ force }) => {
      const stream = streamRef.current;
      if (!stream) return;
      if (force) autoScrollRef.current = true;
      if (autoScrollRef.current) stream.scrollTop = stream.scrollHeight;
    });
  }, []);

  // 每次转录提交后:优先应用分页锚点(保持阅读位置),否则按 autoScroll 跟随。
  useLayoutEffect(() => {
    const stream = streamRef.current;
    if (!stream) return;
    const anchor = anchorRef.current;
    if (anchor) {
      anchorRef.current = null;
      if (appState.transcript.length > anchor.count) {
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

  return (
    <div
      ref={streamRef}
      className="min-h-0 flex-1 overflow-y-auto pt-5 pb-3"
      onScroll={() => {
        const stream = streamRef.current;
        if (!stream) return;
        autoScrollRef.current = stream.scrollHeight - stream.scrollTop - stream.clientHeight < 80;
        if (stream.scrollTop < 80 && appState.transcriptNextCursor && !appState.transcriptLoading) void loadOlder();
      }}
    >
      <div className="flex justify-center">
        {appState.transcriptNextCursor ? (
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
        {appState.transcript.map((item) => (
          <TranscriptEntry key={item.key} item={item} live={item.key === liveThoughtKey} />
        ))}
      </div>
    </div>
  );
}
