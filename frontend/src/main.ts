import { createApp } from 'vue'
import { createPinia } from 'pinia'
import 'element-plus/dist/index.css'
import App from './App.vue'
import { TOKEN_KEY } from './api'
import router from './router'
import './style.css'

function mount() {
  createApp(App).use(createPinia()).use(router).mount('#app')
}

if (import.meta.env.MODE === 'showcase') {
  void import('./showcase/support').then(({ demoActor, demoToken }) => {
    if (!demoActor(localStorage.getItem(TOKEN_KEY))) {
      localStorage.setItem(TOKEN_KEY, demoToken('customer'))
    }
    mount()
  })
} else {
  mount()
}
