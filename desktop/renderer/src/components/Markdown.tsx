// Agent 文本的 Markdown 渲染:react-markdown + remark-gfm。
// - 默认不渲染原始 HTML(不引入任何 raw-html 渲染插件),保持无注入面;
// - 链接拦截点击并经 window.open 交给 Electron 的 setWindowOpenHandler
//   路由到外部浏览器,绝不在应用窗口内导航;
// - 代码块带复制按钮;远程图片受 CSP 限制,降级为来源标记 chip。

import { memo, useRef, type ReactNode } from 'react';
import { Copy } from 'lucide-react';
import ReactMarkdown, { type Components } from 'react-markdown';
import remarkGfm from 'remark-gfm';

import { t } from '@/core/i18n';
import { toast } from '@/core/ui-host';

function CodeBlock({ children }: { children?: ReactNode }) {
  const preRef = useRef<HTMLPreElement>(null);
  return (
    <div className="group/code relative">
      <pre ref={preRef} className="md-pre">
        {children}
      </pre>
      <button
        type="button"
        aria-label={t('chat.copyCode')}
        title={t('chat.copyCode')}
        className="absolute top-1.5 right-1.5 flex size-6 items-center justify-center rounded-md border border-border bg-card/90 text-muted-foreground opacity-0 transition-opacity group-hover/code:opacity-100 focus-visible:opacity-100 hover:text-strong"
        onClick={() => {
          const text = preRef.current?.innerText || '';
          void navigator.clipboard.writeText(text).then(() => toast(t('chat.copied')));
        }}
      >
        <Copy className="size-3.5" />
      </button>
    </div>
  );
}

const components: Components = {
  a: ({ href, children }) => (
    <a
      href={href}
      className="md-link"
      onClick={(event) => {
        // 应用窗口永不导航;http(s) 由主进程 open handler 转外部浏览器。
        event.preventDefault();
        if (href) window.open(href, '_blank', 'noopener');
      }}
    >
      {children}
    </a>
  ),
  img: ({ src, alt }) =>
    src && (src.startsWith('data:') || src.startsWith('blob:') || src.startsWith('file:')) ? (
      <img src={src} alt={alt || ''} className="md-img" />
    ) : (
      <span className="md-img-chip" title={src || undefined}>
        {alt || src || 'image'}
      </span>
    ),
  pre: ({ children }) => <CodeBlock>{children}</CodeBlock>,
};

export const Markdown = memo(function Markdown({ text }: { text: string }) {
  return (
    <div className="md-body">
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={components}>
        {text}
      </ReactMarkdown>
    </div>
  );
});
