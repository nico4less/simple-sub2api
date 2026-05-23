import { createApp } from 'vue'
import { createRouter, createWebHistory } from 'vue-router'

import App from './App.vue'
import i18n from './i18n'
import './styles.css'
import AccountsView from './views/AccountsView.vue'
import DashboardView from './views/DashboardView.vue'
import GroupsView from './views/GroupsView.vue'
import LoginView from './views/LoginView.vue'
import ProxiesView from './views/ProxiesView.vue'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', redirect: '/dashboard' },
    { path: '/login', component: LoginView, meta: { public: true } },
    { path: '/dashboard', component: DashboardView },
    { path: '/keys', redirect: '/dashboard' },
    { path: '/admin/groups', component: GroupsView },
    { path: '/admin/accounts', component: AccountsView },
    { path: '/admin/proxies', component: ProxiesView },
    { path: '/:pathMatch(.*)*', redirect: '/dashboard' }
  ]
})

createApp(App).use(router).use(i18n).mount('#app')
