<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import {
		Table,
		TableBody,
		TableCell,
		TableHead,
		TableHeader,
		TableRow
	} from '$lib/components/ui/table';
	import { createSvelteTable } from '$lib/components/ui/data-table';
	import {
		getCoreRowModel,
		getSortedRowModel,
		type ColumnDef,
		type SortingState
	} from '@tanstack/table-core';
	import { formatDuration, formatNumber, timeAgo } from '$lib/utils';
	import type { Repo, PullRequest, Review, User } from '$lib/types';

	let { data } = $props();
	const d = $derived(data.data);
	const activeTab = $derived(data.tab ?? 'repos');

	// ── Sorting state ───────────────────────────────────────────────────────────
	let sorting = $state<SortingState>([]);

	// ── Column definitions ──────────────────────────────────────────────────────
	const repoColumns: ColumnDef<Repo>[] = [
		{ id: 'FullName', accessorKey: 'FullName', header: 'Repo', enableSorting: true },
		{ id: 'MergedPRCount', accessorKey: 'MergedPRCount', header: 'Merged PRs', enableSorting: true },
		{ id: 'AvgMergeTimeSecs', accessorKey: 'AvgMergeTimeSecs', header: 'Avg Time', enableSorting: true },
		{ id: 'Stars', accessorKey: 'Stars', header: 'Stars', enableSorting: true },
		{ id: 'SyncStatus', accessorKey: 'SyncStatus', header: 'Status', enableSorting: false }
	];

	const prColumns: ColumnDef<PullRequest>[] = [
		{ id: 'RepoFullName', accessorKey: 'RepoFullName', header: 'Repo', enableSorting: true },
		{ id: 'Number', accessorKey: 'Number', header: '#', enableSorting: true },
		{ id: 'Title', accessorKey: 'Title', header: 'Title', enableSorting: false },
		{ id: 'AuthorLogin', accessorKey: 'AuthorLogin', header: 'Author', enableSorting: true },
		{ id: 'MergeTimeSecs', accessorKey: 'MergeTimeSecs', header: 'Merge Time', enableSorting: true }
	];

	const reviewColumns: ColumnDef<Review>[] = [
		{ id: 'RepoFullName', accessorKey: 'RepoFullName', header: 'Repo', enableSorting: true },
		{ id: 'PRNumber', accessorKey: 'PRNumber', header: 'PR', enableSorting: true },
		{ id: 'ReviewerLogin', accessorKey: 'ReviewerLogin', header: 'Reviewer', enableSorting: true },
		{ id: 'State', accessorKey: 'State', header: 'State', enableSorting: true },
		{ id: 'SubmittedAt', accessorKey: 'SubmittedAt', header: 'Submitted', enableSorting: true }
	];

	const userColumns: ColumnDef<User>[] = [
		{ id: 'Login', accessorKey: 'Login', header: 'User', enableSorting: true },
		{ id: 'Followers', accessorKey: 'Followers', header: 'Followers', enableSorting: true },
		{ id: 'PublicRepos', accessorKey: 'PublicRepos', header: 'Public Repos', enableSorting: true }
	];

	// ── Table instances ─────────────────────────────────────────────────────────
	// eslint-disable-next-line @typescript-eslint/no-explicit-any
	function makeTable(rows: any[], columns: ColumnDef<any>[]) {
		return createSvelteTable({
			data: rows,
			columns,
			state: { sorting },
			onSortingChange: (updater) => {
				sorting = typeof updater === 'function' ? updater(sorting) : updater;
			},
			getCoreRowModel: getCoreRowModel(),
			getSortedRowModel: getSortedRowModel()
		});
	}

	const table = $derived.by(() => {
		if (activeTab === 'prs') return makeTable(d?.PRs ?? [], prColumns);
		if (activeTab === 'reviews') return makeTable(d?.Reviews ?? [], reviewColumns);
		if (activeTab === 'users') return makeTable(d?.Users ?? [], userColumns);
		return makeTable(d?.Repos ?? [], repoColumns);
	});

	// ── Navigation helpers ──────────────────────────────────────────────────────
	const tabs = [
		{ key: 'repos', label: 'Repos' },
		{ key: 'prs', label: 'PRs' },
		{ key: 'reviews', label: 'Reviews' },
		{ key: 'users', label: 'Users' }
	];

	function setTab(tab: string) {
		sorting = [];
		goto(`?tab=${tab}`, { invalidateAll: true });
	}

	let searchVal = $state($page.url.searchParams.get('search') ?? '');
	let searchTimer: ReturnType<typeof setTimeout>;

	function onSearch(e: Event) {
		const val = (e.target as HTMLInputElement).value;
		searchVal = val;
		clearTimeout(searchTimer);
		searchTimer = setTimeout(() => {
			const u = new URL($page.url);
			u.searchParams.set('search', val);
			u.searchParams.set('page', '0');
			goto(u.toString(), { invalidateAll: true });
		}, 400);
	}

	function navPage(p: number) {
		const u = new URL($page.url);
		u.searchParams.set('page', String(p));
		goto(u.toString(), { invalidateAll: true });
	}

	function toggleSort(columnId: string) {
		sorting = sorting[0]?.id === columnId
			? [{ id: columnId, desc: !sorting[0].desc }]
			: [{ id: columnId, desc: false }];
	}

	function sortIcon(columnId: string) {
		if (sorting[0]?.id !== columnId) return '↕';
		return sorting[0].desc ? '↓' : '↑';
	}
</script>

<svelte:head>
	<title>Data Explorer — ngmi</title>
</svelte:head>

<!-- Breadcrumb -->
<div class="mb-3 flex items-center gap-1 text-xs text-muted-foreground">
	<a href="/" class="hover:text-foreground">ngmi</a>
	<span>/</span>
	<span class="text-foreground">Data</span>
</div>

<div class="mb-6">
	<h1 class="mb-1 text-base font-bold">Data Explorer</h1>
	<p class="text-xs text-muted-foreground">Browse all tracked repos, PRs, reviews, and users.</p>
</div>

<!-- Tabs -->
<div class="mb-4 flex gap-1 border-b border-border">
	{#each tabs as tab}
		<button
			onclick={() => setTab(tab.key)}
			class="px-3 py-1.5 text-xs {activeTab === tab.key
				? 'border-b-2 border-foreground font-medium text-foreground'
				: 'text-muted-foreground hover:text-foreground'}"
		>
			{tab.label}
		</button>
	{/each}
</div>

<!-- Search -->
<div class="mb-4 max-w-sm">
	<Input
		type="text"
		placeholder="Search…"
		class="font-mono text-xs"
		value={searchVal}
		oninput={onSearch}
	/>
</div>

<!-- DataTable -->
<div class="rounded-md border border-border">
	<Table>
		<TableHeader>
			{#each table.getHeaderGroups() as headerGroup}
				<TableRow>
					{#each headerGroup.headers as header}
						<TableHead
							class={header.column.getCanSort() ? 'cursor-pointer select-none' : ''}
							onclick={header.column.getCanSort() ? () => toggleSort(header.column.id) : undefined}
						>
							<span class="flex items-center gap-1 text-xs">
								{header.column.columnDef.header}
								{#if header.column.getCanSort()}
									<span class="text-muted-foreground">{sortIcon(header.column.id)}</span>
								{/if}
							</span>
						</TableHead>
					{/each}
				</TableRow>
			{/each}
		</TableHeader>
		<TableBody>
			{#if table.getRowModel().rows.length === 0}
				<TableRow>
					<TableCell colspan={table.getAllColumns().length} class="py-8 text-center text-xs text-muted-foreground">
						No results.
					</TableCell>
				</TableRow>
			{:else}
				{#each table.getRowModel().rows as row}
					<TableRow>
						{#each row.getVisibleCells() as cell}
							<TableCell class="text-xs">
								{@const val = cell.getValue()}
								{@const colId = cell.column.id}

								{#if activeTab === 'repos'}
									{#if colId === 'FullName'}
										<a href="/repo/{val}" class="font-mono hover:underline">{val}</a>
									{:else if colId === 'MergedPRCount'}
										{formatNumber(val as number)}
									{:else if colId === 'AvgMergeTimeSecs'}
										{val ? formatDuration(val as number) : '—'}
									{:else if colId === 'Stars'}
										{val ? `${formatNumber(val as number)} ★` : '—'}
									{:else if colId === 'SyncStatus'}
										<Badge
											variant={(val as string) === 'done' ? 'outline' : 'secondary'}
											class={(val as string) === 'done' ? 'text-green-400' : ''}
										>
											{val}
										</Badge>
									{/if}

								{:else if activeTab === 'prs'}
									{#if colId === 'RepoFullName'}
										<a href="/repo/{val}" class="font-mono hover:underline">{val}</a>
									{:else if colId === 'Number'}
										<span class="font-mono text-muted-foreground">#{val}</span>
									{:else if colId === 'Title'}
										<a
											href="https://github.com/{row.getValue('RepoFullName')}/pull/{row.getValue('Number')}"
											target="_blank"
											rel="noopener"
											class="block max-w-xs truncate hover:underline"
										>
											{val}
										</a>
									{:else if colId === 'AuthorLogin'}
										<a href="/user/{val}" class="font-mono hover:underline">@{val}</a>
									{:else if colId === 'MergeTimeSecs'}
										{val ? formatDuration(val as number) : '—'}
									{/if}

								{:else if activeTab === 'reviews'}
									{#if colId === 'RepoFullName'}
										<a href="/repo/{val}" class="font-mono hover:underline">{val}</a>
									{:else if colId === 'PRNumber'}
										<span class="font-mono text-muted-foreground">#{val}</span>
									{:else if colId === 'ReviewerLogin'}
										<a href="/user/{val}" class="font-mono hover:underline">@{val}</a>
									{:else if colId === 'State'}
										<Badge
											variant={(val as string) === 'APPROVED' ? 'outline' : 'secondary'}
											class={(val as string) === 'APPROVED'
												? 'text-green-400'
												: (val as string) === 'CHANGES_REQUESTED'
													? 'text-red-400'
													: ''}
										>
											{val}
										</Badge>
									{:else if colId === 'SubmittedAt'}
										<span class="text-muted-foreground">{timeAgo(val as string)}</span>
									{/if}

								{:else if activeTab === 'users'}
									{#if colId === 'Login'}
										<a href="/user/{val}" class="flex items-center gap-2 hover:underline">
											{#if (row.original as User).AvatarURL}
												<img src={(row.original as User).AvatarURL} alt="" class="size-5 rounded-full" />
											{/if}
											<span class="font-mono">@{val}</span>
											{#if (row.original as User).Name}
												<span class="text-muted-foreground">{(row.original as User).Name}</span>
											{/if}
										</a>
									{:else if colId === 'Followers'}
										{formatNumber(val as number)}
									{:else if colId === 'PublicRepos'}
										{formatNumber(val as number)}
									{/if}
								{/if}
							</TableCell>
						{/each}
					</TableRow>
				{/each}
			{/if}
		</TableBody>
	</Table>
</div>

<!-- Pagination -->
<div class="mt-4 flex items-center justify-between text-xs text-muted-foreground">
	<span>{formatNumber(d?.Total ?? 0)} total</span>
	<div class="flex gap-2">
		{#if d?.HasPrev}
			<Button variant="outline" size="sm" onclick={() => navPage(d.PrevPage)}>← Prev</Button>
		{/if}
		{#if d?.HasNext}
			<Button variant="outline" size="sm" onclick={() => navPage(d.NextPage)}>Next →</Button>
		{/if}
	</div>
</div>
