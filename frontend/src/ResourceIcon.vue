<script setup lang="ts">
defineProps<{ name: 'gpu' | 'cpu' | 'memory' | 'disk' }>()
const paths = {
  gpu: 'M3 6h18v12H3z M7 9h7v6H7z M17 9v6 M6 18v3 M10 18v3 M14 18v3',
  cpu: 'M6 6h12v12H6z M9 9h6v6H9z M9 3v3 M15 3v3 M9 18v3 M15 18v3 M3 9h3 M3 15h3 M18 9h3 M18 15h3',
  memory: 'M3 7h18v10H3z M6 10h3v4H6z M12 10h3v4h-3z M18 10v4 M6 17v3 M10 17v3 M14 17v3 M18 17v3',
  disk: 'M5 3h14v18H5z M8 7h8 M8 11h8 M8 16h1 M15 16h1',
}
</script>
<template><svg class="icon" viewBox="0 0 24 24" aria-hidden="true"><path :d="paths[name]" /></svg></template>
