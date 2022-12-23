import { createApp } from "vue";
import App from "./App.vue";
import router from "./router";
import zstd from "zstandard-wasm";

import "./assets/main.css";

await zstd.loadWASM();
const app = createApp(App);

app.use(router);

app.mount("#app");
