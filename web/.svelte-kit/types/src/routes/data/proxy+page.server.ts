// @ts-nocheck
import type { PageServerLoad } from './$types';

export const load = async ({ fetch, url }: Parameters<PageServerLoad>[0]) => {
	const tab = url.searchParams.get('tab') ?? 'repos';
	const page = url.searchParams.get('page') ?? '0';
	const search = url.searchParams.get('search') ?? '';
	const sortBy = url.searchParams.get('sort') ?? '';
	const author = url.searchParams.get('author') ?? '';
	const reviewer = url.searchParams.get('reviewer') ?? '';
	const state = url.searchParams.get('state') ?? '';
	const repo = url.searchParams.get('repo') ?? '';
	const status = url.searchParams.get('status') ?? '';

	const qs = new URLSearchParams({ page, search, sort: sortBy, author, reviewer, state, repo, status });

	let endpoint: string;
	if (tab === 'prs') endpoint = `/api/v1/data/prs?${qs}`;
	else if (tab === 'reviews') endpoint = `/api/v1/data/reviews?${qs}`;
	else if (tab === 'users') endpoint = `/api/v1/data/users?${qs}`;
	else endpoint = `/api/v1/data/repos?${qs}`;

	const resp = await fetch(endpoint);
	if (!resp.ok) return { data: null, tab };
	const data = await resp.json();
	return { data, tab };
};
