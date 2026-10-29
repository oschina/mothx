<script>
  import { createEventDispatcher, tick } from 'svelte';
  import { t } from '../lib/preferences.js';
  import { Input } from '$lib/components/ui/input/index.js';
  import { Button } from '$lib/components/ui/button/index.js';
  import { ChevronsUpDown, FileText, Image as ImageIcon, Mic, Type, Video } from '@lucide/svelte';

  export let value = '';
  export let options = [];
  export let placeholder = '';
  export let ariaLabel = '';
  export let disabled = false;
  export let className = '';
  export let noOptionsLabel = '';

  const dispatch = createEventDispatcher();
  let query = '';
  let open = false;
  let input;
  let root;

  function modelLabel(model) {
    return model?.name || model?.id || '';
  }

  $: selected = options.find((option) => option.id === value) || null;
  $: displayValue = open ? query : (selected ? modelLabel(selected) : value || '');
  $: normalizedQuery = query.trim().toLowerCase();
  $: filteredOptions = normalizedQuery
    ? options.filter((option) => {
        const haystack = `${option.id} ${option.name || ''} ${option.provider || ''}`.toLowerCase();
        return haystack.includes(normalizedQuery);
      })
    : options;

  function openPicker() {
    if (disabled) return;
    query = '';
    open = true;
    tick().then(() => {
      if (open) root?.querySelector('input')?.focus();
    });
  }

  function closePicker() {
    open = false;
    query = '';
  }

  function choose(option) {
    value = option.id;
    closePicker();
    dispatch('change', value);
  }

  function handleWindowClick(event) {
    if (!root?.contains(event.target)) closePicker();
  }

  function handleKeydown(event) {
    if (event.key === 'Escape') {
      closePicker();
      return;
    }
    if (event.key === 'Enter' && filteredOptions.length > 0) {
      event.preventDefault();
      choose(filteredOptions[0]);
    }
  }

  function modalityIcon(kind) {
    if (kind === 'image') return ImageIcon;
    if (kind === 'text') return Type;
    if (kind === 'audio') return Mic;
    if (kind === 'video') return Video;
    if (kind === 'file') return FileText;
    return null;
  }

  function modalityClass(kind) {
    if (kind === 'text') return 'modality-text';
    if (kind === 'image') return 'modality-image';
    return 'modality-other';
  }

  function modalityLabel(kind) {
    const key = `chat.modality.${kind}`;
    const translated = $t(key);
    if (translated !== key) return translated;
    if (typeof kind !== 'string') return String(kind ?? '');
    return kind.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase());
  }
</script>

<div bind:this={root} class={`model-search-select ${className}`} class:open class:disabled>
  <Input
    bind:this={input}
    value={displayValue}
    {placeholder}
    aria-label={ariaLabel}
    aria-expanded={open}
    aria-haspopup="listbox"
    autocomplete="off"
    {disabled}
    onfocus={openPicker}
    oninput={(event) => { query = event.currentTarget.value; open = true; }}
    onkeydown={handleKeydown}
  />
  <Button
    type="button"
    variant="ghost"
    size="icon-xs"
    class="model-search-select-chevron"
    aria-label={open ? $t('common.close') : $t('common.open')}
    {disabled}
    onclick={() => { open ? closePicker() : openPicker(); }}
  >
    <ChevronsUpDown size={14} aria-hidden="true" />
  </Button>
  {#if open}
    <div class="model-search-select-menu" role="listbox" tabindex="-1">
      {#if filteredOptions.length === 0}
        <div class="model-search-select-empty">{noOptionsLabel || $t('common.noMatchingOptions')}</div>
      {:else}
        {#each filteredOptions as option (option.id)}
          <button
            type="button"
            class="model-search-select-option"
            class:active={option.id === value}
            role="option"
            aria-selected={option.id === value}
            on:mousedown={(event) => { event.preventDefault(); choose(option); }}
          >
            <span class="model-search-select-label">{modelLabel(option)}</span>
            <span class="model-search-select-badges">
              {#if Array.isArray(option.input) && option.input.length > 0}
                {#each option.input as kind}
                  <span class={`model-modality-badge ${modalityClass(kind)}`} title={kind}>
                    {#if modalityIcon(kind)}
                      <svelte:component this={modalityIcon(kind)} size={10} aria-hidden="true" />
                    {/if}
                    <span>{modalityLabel(kind)}</span>
                  </span>
                {/each}
              {/if}
            </span>
          </button>
        {/each}
      {/if}
    </div>
  {/if}
</div>

<svelte:window on:click={handleWindowClick} />
