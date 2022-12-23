<style scoped></style>

<script lang="ts" setup>
import { reactive, onBeforeUnmount, ref } from "vue";
import zstd from "zstandard-wasm";
import { archiveteam_tracker } from "../model/pb.js";
import { useRouter, useRoute } from "vue-router";
import ProjectTracker from "@/components/ProjectTracker.vue";

let launchSocket = (project: string): WebSocket => {
  const proto = window.location.protocol === "https:" ? "wss" : "ws";
  const socket = new WebSocket(proto + "://" + document.location.host + "/ws");
  const openTime = new Date();

  socket.onopen = () => {
    console.log("Connected!");
    socket.send(
      JSON.stringify({
        type: "subscribe",
        project: project,
      })
    );
  };

  socket.onmessage = async (event) => {
    let data: Blob = event.data;
    let buf = await data.arrayBuffer();
    let decompressed = zstd.decompress(new Uint8Array(buf));
    let parsed = archiveteam_tracker.TrackerEvent.decode(decompressed);
    let parsedProject = parsed.project;

    if (!items[parsedProject]) {
      items[parsedProject] = [];
    }

    if (items[parsedProject].length > 15) {
      items[parsedProject].shift();
    }
    items[parsedProject].push(parsed);

    itemCount.value += 1;
    if (Math.random() * 100 < 5) {
      console.log(parsed);
    }
  };

  return socket;
};

const router = useRouter();
const route = useRoute();
const socket = ref(launchSocket(route.params.project));
const items: { [key: string]: archiveteam_tracker.TrackerEvent[] } = reactive(
  {}
);
const itemCount = ref(0);

onBeforeUnmount(() => {
  console.log("unmounting");
  socket.value.close();
});
// 1
</script>

<style scoped>
.projects {
  display: flex;
  flex-wrap: wrap;
}

.project {
  max-width: 25%;
  flex: 1 1 24%;
}
</style>

<template>
  <div class="about">
    <div class="item">
      <h1>{{ $route.params.project }}</h1>
      <div>
        <h1>Events</h1>
        <p>
          <span class="green">{{ itemCount }}</span> events have been recorded
          for this project.
        </p>
        <div class="projects">
          <div v-for="(items, project) in items" :key="project" class="project">
            <ProjectTracker :items="items" :project="project" />
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
