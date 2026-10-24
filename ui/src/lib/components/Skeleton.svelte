<script lang="ts">
  interface Props {
    width?: string;
    height?: string;
    radius?: string;
    className?: string;
  }

  let { width = '100%', height = '12px', radius = '6px', className = '' }: Props = $props();
</script>

<span
  class="skeleton {className}"
  style:width
  style:height
  style:border-radius={radius}
  aria-hidden="true"
></span>

<style>
  .skeleton {
    display: block;
    max-width: 100%;
    background: linear-gradient(
      90deg,
      var(--surface-2) 0%,
      var(--surface-3) 45%,
      var(--surface-2) 100%
    );
    background-size: 220% 100%;
    animation: shimmer 1.5s ease-in-out infinite;
  }

  @keyframes shimmer {
    0% {
      background-position: 120% 0;
    }
    100% {
      background-position: -120% 0;
    }
  }
</style>
