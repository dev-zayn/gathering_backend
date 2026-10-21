import { mdxAnnotations } from 'mdx-annotations'
import remarkGfm from 'remark-gfm'

// `{{ title: 'cURL' }}` after a fence or a heading becomes a prop on it.
export const remarkPlugins = [mdxAnnotations.remark, remarkGfm]
