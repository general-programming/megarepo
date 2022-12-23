<script lang="ts" setup>
import { archiveteam_tracker } from "../model/pb.js";
import { filesize } from "filesize";

defineProps<{
  project: string;
  items: archiveteam_tracker.TrackerEvent[];
}>();
</script>

<template>
  <h1 class="green">{{ project }}</h1>
  <div class="items">
    <div v-for="item in items" :key="item.item">
      <p>
        {{ item.downloader }} -
        {{ filesize(item.bytes, { base: 2, standard: "jedec" }) }} -
        {{
          item.items.length > 1
            ? item.items.length +
              " items" +
              (item.moveItems.length !== item.items.length
                ? " (" + (item.items.length - item.moveItems.length) + " dupes)"
                : "")
            : item.item
        }}
      </p>
    </div>
  </div>
</template>

<style scoped>
h1 {
  font-weight: 500;
  font-size: 2.6rem;
  top: -10px;
}

h3 {
  font-size: 1.2rem;
}
</style>
