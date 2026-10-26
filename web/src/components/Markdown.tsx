import Markdown, { defaultUrlTransform } from "react-markdown";

// What the Grão-Mestre writes is Markdown, shown with no active HTML: a tag the agent writes is dropped
// (never turned into a tag), a link is only http, https or mailto and opens in another page with no
// referrer, an image is never loaded (it would call a site chosen by the agent) and a heading
// is a paragraph, since each screen has one h1 of its own.

const heading = ({ children }: { children?: React.ReactNode }) => (
  <p className="font-display font-semibold">{children}</p>
);

const safeUrl = (url: string) => {
  const safe = defaultUrlTransform(url);
  return /^(https?:|mailto:)/i.test(safe) ? safe : "";
};

export function ChatMarkdown({ children }: { children: string }) {
  return (
    <div className="chat-md flex flex-col gap-2 break-words">
      <Markdown
        skipHtml
        urlTransform={safeUrl}
        disallowedElements={["img"]}
        unwrapDisallowed
        components={{
          h1: heading,
          h2: heading,
          h3: heading,
          h4: heading,
          h5: heading,
          h6: heading,
          a: ({ href, children }) =>
            href ? (
              <a
                href={href}
                target="_blank"
                rel="noopener noreferrer nofollow"
                className="text-accent underline"
              >
                {children}
              </a>
            ) : (
              <span>{children}</span>
            ),
          pre: ({ children }) => (
            <pre className="overflow-x-auto rounded-field bg-paper p-2 font-mono text-sm">
              {children}
            </pre>
          ),
          code: ({ children }) => <code className="font-mono text-[0.9em]">{children}</code>,
          ul: ({ children }) => <ul className="list-disc pl-5">{children}</ul>,
          ol: ({ children }) => <ol className="list-decimal pl-5">{children}</ol>,
          blockquote: ({ children }) => (
            <blockquote className="border-l-2 border-line pl-3 text-muted">{children}</blockquote>
          ),
          table: ({ children }) => (
            <div className="overflow-x-auto">
              <table className="text-sm">{children}</table>
            </div>
          ),
          th: ({ children }) => (
            <th className="border border-line px-2 py-1 text-left">{children}</th>
          ),
          td: ({ children }) => <td className="border border-line px-2 py-1">{children}</td>,
        }}
      >
        {children}
      </Markdown>
    </div>
  );
}
