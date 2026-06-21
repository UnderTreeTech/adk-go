package filegentool

import (
	"bytes"
	"fmt"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// renderMarkdownToHTML converts markdown source to a self-contained HTML
// document with embedded CSS styling. The output is a complete HTML5 page
// suitable for saving directly to the artifact service.
// The title parameter is used as the <title> element value.
func renderMarkdownToHTML(mdSource string, title string) ([]byte, error) {
	md := goldmark.New(
		goldmark.WithExtensions(extension.NewTable()),
	)

	var buf bytes.Buffer
	if err := md.Convert([]byte(mdSource), &buf); err != nil {
		return nil, err
	}

	body := buf.String()
	doc := htmlDocument(body, title)
	return []byte(doc), nil
}

// htmlDocument wraps rendered HTML body content in a complete HTML5 document
// with professional styling.
// The title parameter is used as the <title> element value.
func htmlDocument(body string, title string) string {
	const tpl = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>%s</title>
<style>
body {
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, "Noto Sans SC", sans-serif;
    font-size: 16px;
    line-height: 1.7;
    color: #333;
    max-width: 900px;
    margin: 0 auto;
    padding: 40px 20px;
    background: #fff;
}
h1 {
    font-size: 2em;
    text-align: center;
    border-bottom: 2px solid #e0e0e0;
    padding-bottom: 10px;
    margin-top: 1.5em;
    margin-bottom: 0.8em;
}
h2 {
    font-size: 1.6em;
    border-bottom: 1px solid #e8e8e8;
    padding-bottom: 6px;
    margin-top: 1.4em;
    margin-bottom: 0.6em;
}
h3 { font-size: 1.3em; margin-top: 1.2em; margin-bottom: 0.5em; }
h4 { font-size: 1.1em; margin-top: 1em; margin-bottom: 0.4em; }
h5 { font-size: 1em; margin-top: 0.9em; margin-bottom: 0.3em; }
h6 { font-size: 0.9em; margin-top: 0.8em; margin-bottom: 0.3em; color: #666; }
p { margin: 0.6em 0; }
a { color: #0366d6; text-decoration: none; }
a:hover { text-decoration: underline; }
code {
    font-family: "SFMono-Regular", Consolas, "Liberation Mono", Menlo, monospace;
    background: #f0f0f0;
    padding: 2px 6px;
    border-radius: 3px;
    font-size: 0.9em;
}
pre {
    background: #f5f5f5;
    border: 1px solid #e0e0e0;
    border-radius: 4px;
    padding: 12px 16px;
    overflow-x: auto;
    line-height: 1.5;
}
pre code {
    background: none;
    padding: 0;
    font-size: 0.88em;
}
ul, ol { padding-left: 2em; margin: 0.5em 0; }
li { margin: 0.25em 0; }
table {
    border-collapse: collapse;
    width: 100%%;
    margin: 1em 0;
    font-size: 0.95em;
}
th, td {
    border: 1px solid #d0d0d0;
    padding: 8px 12px;
    text-align: left;
}
th {
    background: #f0f0f0;
    font-weight: 600;
}
tr:nth-child(even) td { background: #fafafa; }
hr {
    border: none;
    border-top: 1px solid #ddd;
    margin: 1.5em 0;
}
blockquote {
    border-left: 4px solid #ddd;
    padding-left: 16px;
    color: #666;
    margin: 1em 0;
}
img { max-width: 100%%; height: auto; }
</style>
</head>
<body>
%s
</body>
</html>`
	return fmt.Sprintf(tpl, title, body)
}
