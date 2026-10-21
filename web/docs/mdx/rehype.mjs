import { slugifyWithCounter } from '@sindresorhus/slugify'
import * as acorn from 'acorn'
import { toString } from 'mdast-util-to-string'
import { mdxAnnotations } from 'mdx-annotations'
import shiki from 'shiki'
import { visit } from 'unist-util-visit'

// Every fence is coloured at build time, so the page ships as HTML and the
// browser loads no highlighter. The theme paints with CSS variables, which
// the stylesheet sets for the graphite code panel.

function rehypeParseCodeBlocks() {
  return (tree) => {
    visit(tree, 'element', (node, _i, parent) => {
      if (node.tagName === 'code') {
        parent.properties.language = node.properties.className
          ? node.properties.className[0]?.replace(/^language-/, '')
          : 'txt'
      }
    })
  }
}

let highlighter

function rehypeShiki() {
  return async (tree) => {
    highlighter = highlighter ?? (await shiki.getHighlighter({ theme: 'css-variables' }))
    visit(tree, 'element', (node) => {
      if (node.tagName === 'pre' && node.children[0]?.tagName === 'code') {
        const codeNode = node.children[0]
        const textNode = codeNode.children[0]
        node.properties.code = textNode.value
        if (node.properties.language) {
          const tokens = highlighter.codeToThemedTokens(textNode.value, node.properties.language)
          textNode.value = shiki.renderToHtml(tokens, {
            elements: {
              pre: ({ children }) => children,
              code: ({ children }) => children,
              line: ({ children }) => `<span>${children}</span>`,
            },
          })
        }
      }
    })
  }
}

// Headings get ids from their words, so a link to a section survives a
// rebuild. Only h2 and h3: the navigation and the right rail list h2s.
function rehypeSlugify() {
  return (tree) => {
    const slugify = slugifyWithCounter()
    visit(tree, 'element', (node) => {
      if ((node.tagName === 'h2' || node.tagName === 'h3') && !node.properties.id) {
        node.properties.id = slugify(toString(node))
      }
    })
  }
}

// `export const sections = [...]` on every page, listing its h2s, unless the
// page wrote its own.
function rehypeAddMDXExports(getExports) {
  return (tree) => {
    for (const [name, value] of Object.entries(getExports(tree))) {
      const already = tree.children.some(
        (node) =>
          node.type === 'mdxjsEsm' &&
          new RegExp(`export\\s+const\\s+${name}\\s*=`).test(node.value),
      )
      if (already) continue
      const exportStr = `export const ${name} = ${value}`
      tree.children.push({
        type: 'mdxjsEsm',
        value: exportStr,
        data: { estree: acorn.parse(exportStr, { sourceType: 'module', ecmaVersion: 'latest' }) },
      })
    }
  }
}

function getSections(node) {
  const sections = []
  for (const child of node.children ?? []) {
    if (child.type === 'element' && child.tagName === 'h2') {
      sections.push(`{
        title: ${JSON.stringify(toString(child))},
        id: ${JSON.stringify(child.properties.id)},
        ...${child.properties.annotation ?? '{}'}
      }`)
    } else if (child.children) {
      sections.push(...getSections(child))
    }
  }
  return sections
}

export const rehypePlugins = [
  mdxAnnotations.rehype,
  rehypeParseCodeBlocks,
  rehypeShiki,
  rehypeSlugify,
  [rehypeAddMDXExports, (tree) => ({ sections: `[${getSections(tree).join()}]` })],
]
