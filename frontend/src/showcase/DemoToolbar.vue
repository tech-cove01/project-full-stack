<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { TOKEN_KEY } from '../api'
import { useAuthStore } from '../stores/auth'
import { actors, clearDemoData, demoActor, demoToken, type DemoRole } from './support'

const router = useRouter()
const route = useRoute()
const auth = useAuthStore()
const guideOpen = ref(false)
const role = computed(() => demoActor(auth.token)?.role ?? 'customer')
const defaults: Record<DemoRole, string> = {
  customer: '/',
  owner: '/merchant',
  staff: '/merchant/orders',
  reader: '/notes/search',
}

async function openAs(next: DemoRole, path: string) {
  if (next !== role.value) {
    auth.clear()
    localStorage.setItem(TOKEN_KEY, demoToken(next))
    void auth.loadProfile().catch(() => {})
  }
  await router.push(path)
  guideOpen.value = false
}

function switchRole(event: Event) {
  const next = (event.target as HTMLSelectElement).value as DemoRole
  const target = route.name === 'note' || route.name === 'shop' ? route.fullPath : defaults[next]
  void openAs(next, target)
}

function reset() {
  if (!window.confirm('恢复初始示例？这个浏览器中的演示订单、笔记和规则将被清空。')) return
  clearDemoData()
  auth.clear()
  window.location.hash = '#/'
  window.location.reload()
}
</script>

<template>
  <section class="demo-toolbar" aria-label="演示选项">
    <div class="demo-inner">
      <div class="demo-bar">
        <div class="demo-caption">
          <strong>演示环境</strong>
          <span>用示例身份操作，结果保存在当前浏览器。</span>
        </div>
        <div class="demo-controls">
          <label for="showcase-role">当前身份</label>
          <select id="showcase-role" :value="role" @change="switchRole">
            <option v-for="actor in actors" :key="actor.role" :value="actor.role">
              {{ actor.nickName }}
            </option>
          </select>
          <button
            type="button"
            :aria-expanded="guideOpen"
            aria-controls="showcase-guide"
            @click="guideOpen = !guideOpen"
          >
            体验路线
          </button>
          <button type="button" @click="reset">重置示例</button>
          <a href="../">项目介绍</a>
        </div>
      </div>
      <div id="showcase-guide" v-show="guideOpen" class="demo-guide">
        <section>
          <h2>到店订单</h2>
          <p>店主上架后，顾客在街角咖啡抢券并模拟支付。复制核销凭证，换店员核销。</p>
          <div>
            <button type="button" @click="openAs('owner', '/merchant/activities')">店主发券</button>
            <button type="button" @click="openAs('customer', '/shops/1')">顾客买券</button>
            <button type="button" @click="openAs('staff', '/merchant/orders')">店员核销</button>
          </div>
        </section>
        <section>
          <h2>探店社区</h2>
          <p>给街角咖啡写笔记，门店 ID 填 1。发布后留在笔记页，切换成读者，关注作者并点赞。</p>
          <div>
            <button type="button" @click="openAs('customer', '/notes/new')">发布笔记</button>
            <button
              type="button"
              @click="openAs('reader', route.name === 'note' ? route.fullPath : '/notes/1')"
            >
              读者体验
            </button>
            <button type="button" @click="openAs('reader', '/notes/search')">搜索笔记</button>
          </div>
        </section>
        <section>
          <h2>店铺助手</h2>
          <p>试问「街角咖啡停车怎么收费？」或「我的订单」，打开规则出处或订单详情。</p>
          <div>
            <button type="button" @click="openAs('customer', '/assistant')">打开店铺助手</button>
            <button type="button" @click="openAs('owner', '/merchant/knowledge')">
              管理门店规则
            </button>
          </div>
        </section>
      </div>
    </div>
  </section>
</template>

<style scoped>
.demo-toolbar {
  background: #edf2ea;
  border-bottom: 1px solid #dfe8de;
}
.demo-inner {
  max-width: 1200px;
  margin: 0 auto;
  padding: 0 32px;
}
.demo-bar,
.demo-controls,
.demo-caption {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
}
.demo-bar {
  justify-content: space-between;
  padding: 10px 0;
  font-size: 13px;
}
.demo-caption span,
.demo-guide p {
  color: #48615a;
}
.demo-controls label {
  color: #374f47;
}
.demo-controls select {
  border: 1px solid #b9cfc1;
  border-radius: 6px;
  padding: 5px;
  color: #193436;
  background: #fff;
  font: inherit;
}
button,
.demo-controls a {
  border: 0;
  padding: 4px 2px;
  color: #14584c;
  background: transparent;
  font-weight: 600;
  text-decoration: underline;
  text-underline-offset: 3px;
  cursor: pointer;
}
.demo-guide {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 20px;
  padding: 12px 0 18px;
  border-top: 1px solid #d4e0d6;
}
.demo-guide h2 {
  margin: 0 0 6px;
  font-size: 16px;
}
.demo-guide p {
  margin: 0 0 6px;
  font-size: 13px;
  line-height: 1.55;
}
.demo-guide button {
  margin-right: 12px;
  font-size: 13px;
}
@media (max-width: 700px) {
  .demo-inner {
    padding: 0 16px;
  }
  .demo-guide {
    grid-template-columns: minmax(0, 1fr);
    gap: 12px;
  }
}
</style>
