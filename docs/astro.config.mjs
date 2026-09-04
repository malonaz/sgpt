// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';

export default defineConfig({
	site: 'https://docs.sgpt.malonaz.com',
	integrations: [
		starlight({
			title: 'SGPT',
			description: 'A terminal-native AI coding agent.',
			logo: { src: './src/assets/logo.svg', replacesTitle: false },
			customCss: ['./src/styles/theme.css'],
			social: [{ icon: 'github', label: 'GitHub', href: 'https://github.com/malonaz/sgpt' }],
			editLink: { baseUrl: 'https://github.com/malonaz/sgpt/edit/master/docs/' },
			sidebar: [
				{
					label: 'Start',
					items: [
						{ label: 'Introduction', slug: 'guides/introduction' },
						{ label: 'Installation', slug: 'guides/installation' },
						{ label: 'Configuration', slug: 'guides/configuration' },
						{ label: 'First chat', slug: 'guides/first-chat' },
					],
				},
				{
					label: 'Concepts',
					items: [
						{ label: 'How it works', slug: 'concepts/architecture' },
						{ label: 'The .sgpt graph', slug: 'concepts/graph' },
						{ label: 'Roles', slug: 'concepts/roles' },
						{ label: 'Tools & review', slug: 'concepts/tools' },
						{ label: 'Tool engines', slug: 'concepts/tool-engines' },
						{ label: 'Lores', slug: 'concepts/lores' },
						{ label: 'Sub-agents', slug: 'concepts/agents' },
					],
				},
				{
					label: 'Reference',
					items: [
						{ label: 'CLI', slug: 'reference/cli' },
						{ label: 'Keymap', slug: 'reference/keymap' },
						{ label: 'Configuration schema', slug: 'reference/configuration' },
						{ label: 'Role directives', slug: 'reference/role-directives' },
						{ label: 'Built-in tools', slug: 'reference/builtin-tools' },
					],
				},
			],
		}),
	],
});
