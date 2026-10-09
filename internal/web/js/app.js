    function readerApp() {
      return {
        isLoggedIn: false,
        feeds: [],
        collapsedGroups: {},
        selectedCategory: 'all',
        profile: { username: '', auth_token: '' },
        loginForm: { username: 'admin', password: '' },
        
        // 1. 状态收纳 (统一集中管理弹窗和异步加载状态)
        modals: {
          add: false,
          edit: false,
          clientInfo: false,
          tokens: false,
          db: false,
          content: false,
          stagger: false,
          category: false
        },
        loading: {
          db: false,
          dbAction: false,
          batch: false,
          stagger: false,
          category: false,
          tokens: false
        },

        tokensList: [],
        detailArticle: null,
        copySuccess: false,
        toasts: [],
        dbCurrentFeed: {},
        dbArticles: [],
        dbArticlesTotal: 0,
        selectedArticleIds: [],
        selectedFeedIds: [],

        staggerTargetName: '',
        staggerTargetFeeds: [],
        staggerForm: { start_time: '08:00', window_minutes: 30 },
        categoryForm: { current_name: '', name: '', sort_order: 0 },
        modalForm: { id: null, title: '', feed_url: '', category_name: '', schedule_type: 'daily_fixed', schedule_value: '08:00' },

        init() {
          this.checkAuth();
        },

        // 全局 Toast 提示通知
        toast(message, type = 'info', duration = 3000) {
          const id = Date.now() + Math.random().toString(36).substring(2, 6);
          this.toasts.push({ id, message, type });
          setTimeout(() => { this.removeToast(id); }, duration);
        },

        removeToast(id) {
          this.toasts = this.toasts.filter(t => t.id !== id);
        },

        // 统一轻量 API 请求客户端
        async api(url, options = {}) {
          const config = {
            headers: options.body ? { 'Content-Type': 'application/json' } : {},
            ...options
          };
          if (config.body && typeof config.body === 'object') {
            config.body = JSON.stringify(config.body);
          }
          const res = await fetch(url, config);
          let data = null;
          try {
            data = await res.json();
          } catch (_) {}
          if (!res.ok) {
            throw new Error((data && data.error) ? data.error : `请求失败 (${res.status})`);
          }
          return data;
        },

        async checkAuth() {
          try {
            this.profile = await this.api('/api/admin/profile');
            this.isLoggedIn = true;
            this.loadFeeds();
          } catch (_) {
            this.isLoggedIn = false;
          }
        },

        async login() {
          try {
            await this.api('/api/admin/login', { method: 'POST', body: this.loginForm });
            this.toast('登录成功', 'success');
            await this.checkAuth();
          } catch (e) {
            this.toast('登录失败: ' + e.message, 'error');
          }
        },

        async logout() {
          try {
            await this.api('/api/admin/logout', { method: 'POST' });
          } catch (_) {}
          this.isLoggedIn = false;
          this.toast('已安全退出', 'info');
        },

        async loadFeeds() {
          try {
            this.feeds = await this.api('/api/admin/feeds');
            const validIds = new Set(this.feeds.map(f => f.id));
            this.selectedFeedIds = this.selectedFeedIds.filter(id => validIds.has(id));
          } catch (e) {
            this.toast('加载订阅源失败: ' + e.message, 'error');
          }
        },

        // 1. 单次遍历计算 (Single-Pass)：归纳统计和文件夹分组，一次循环计算出全部衍生数据
        get computedData() {
          let dailyFixedCount = 0;
          let pausedCount = 0;
          let unreadTotal = 0;
          const groupsMap = {};

          for (const f of this.feeds) {
            if (f.schedule_type === 'daily_fixed') dailyFixedCount++;
            else if (f.schedule_type === 'paused') pausedCount++;
            unreadTotal += (f.unread_count || 0);

            const catName = f.category_name || '未分类';
            if (!groupsMap[catName]) {
              groupsMap[catName] = {
                name: catName,
                count: 0,
                sortOrder: f.category_name ? (f.category_sort_order ?? 0) : 999999,
                feeds: [],
                unreadTotal: 0
              };
            }
            groupsMap[catName].count++;
            groupsMap[catName].feeds.push(f);
            groupsMap[catName].unreadTotal += (f.unread_count || 0);
          }

          const sortedGroups = Object.values(groupsMap).sort((a, b) => {
            if (a.name === '未分类') return 1;
            if (b.name === '未分类') return -1;
            return a.sortOrder !== b.sortOrder ? a.sortOrder - b.sortOrder : a.name.localeCompare(b.name);
          });

          const categoryList = sortedGroups.map(g => ({ name: g.name, count: g.count }));
          const filteredGroups = sortedGroups
            .filter(g => this.selectedCategory === 'all' || this.selectedCategory === g.name)
            .map(g => ({
              categoryName: g.name,
              sortOrder: g.sortOrder,
              feeds: g.feeds,
              unreadTotal: g.unreadTotal
            }));

          return {
            totalFeeds: this.feeds.length,
            dailyFixedCount,
            pausedCount,
            unreadTotal,
            categoryList,
            filteredGroups
          };
        },

        // 2. 通用选择与全选抽象函数 (DRY Principle)
        isSelected(list, id) {
          return list.includes(id);
        },

        toggleSelect(list, id) {
          const idx = list.indexOf(id);
          if (idx >= 0) list.splice(idx, 1);
          else list.push(id);
        },

        toggleAll(list, sourceList) {
          if (sourceList.length > 0 && sourceList.every(item => list.includes(item.id))) {
            const removeSet = new Set(sourceList.map(item => item.id));
            const remaining = list.filter(id => !removeSet.has(id));
            list.length = 0;
            list.push(...remaining);
          } else {
            const set = new Set(list);
            sourceList.forEach(item => set.add(item.id));
            list.length = 0;
            list.push(...Array.from(set));
          }
        },

        isGroupAllSelected(feeds) {
          return feeds && feeds.length > 0 && feeds.every(f => this.selectedFeedIds.includes(f.id));
        },

        toggleGroupSelect(feeds) {
          this.toggleAll(this.selectedFeedIds, feeds);
        },

        isAllSelected() {
          return this.feeds.length > 0 && this.feeds.every(f => this.selectedFeedIds.includes(f.id));
        },

        toggleSelectAll() {
          this.toggleAll(this.selectedFeedIds, this.feeds);
        },

        editSelectedFeed() {
          if (this.selectedFeedIds.length === 1) {
            const feed = this.feeds.find(f => f.id === this.selectedFeedIds[0]);
            if (feed) this.openEditModal(feed);
          }
        },

        getSelectedFeeds() {
          return this.feeds.filter(f => this.selectedFeedIds.includes(f.id));
        },

        // 3. 执行效率与交互响应优化 (Optimistic UI 乐观立即更新，减少无谓全量刷盘)
        async runBatchAction(action) {
          if (this.selectedFeedIds.length === 0) return;
          if (action === 'delete' && !confirm(`确定要删除选中的 ${this.selectedFeedIds.length} 个订阅源吗？`)) {
            return;
          }

          const targetIds = new Set(this.selectedFeedIds);
          const backupMap = new Map();

          // 乐观更新：暂停与恢复立即变更本地状态
          if (action === 'pause' || action === 'resume') {
            this.feeds.forEach(f => {
              if (targetIds.has(f.id)) {
                backupMap.set(f.id, f.schedule_type);
                f.schedule_type = action === 'pause' ? 'paused' : 'daily_fixed';
              }
            });
            this.toast(action === 'pause' ? '已暂停所选订阅' : '已恢复所选订阅', 'success');
          }

          this.loading.batch = true;
          try {
            const data = await this.api('/api/admin/feeds/batch', {
              method: 'POST',
              body: { action, feed_ids: this.selectedFeedIds }
            });
            if (action === 'fetch') {
              this.toast(`抓取完成，共新增 ${data.total_fetched || 0} 篇文章`, 'success');
              await this.loadFeeds(); // 仅抓取产出新数据时拉取最新状态
            } else if (action === 'delete') {
              this.feeds = this.feeds.filter(f => !targetIds.has(f.id));
              this.selectedFeedIds = [];
              this.toast('已成功删除选中的订阅源', 'success');
            }
          } catch (e) {
            // 回滚
            if (backupMap.size > 0) {
              this.feeds.forEach(f => {
                if (backupMap.has(f.id)) f.schedule_type = backupMap.get(f.id);
              });
            }
            this.toast('操作失败: ' + e.message, 'error');
          } finally {
            this.loading.batch = false;
          }
        },

        // 错峰时间分配
        openStaggerModal(feeds, name) {
          if (!feeds || feeds.length === 0) return this.toast('请先选择订阅源', 'warning');
          this.staggerTargetFeeds = feeds;
          this.staggerTargetName = name || '订阅源';
          this.staggerForm = { start_time: '08:00', window_minutes: 30 };
          this.modals.stagger = true;
        },

        get staggerPreviewList() {
          const n = this.staggerTargetFeeds.length;
          if (n === 0) return [];
          const windowMin = Number(this.staggerForm.window_minutes) || 30;
          const [hStr, mStr] = (this.staggerForm.start_time || '08:00').split(':');
          const startH = parseInt(hStr, 10) || 8;
          const startM = parseInt(mStr, 10) || 0;
          const step = n > 1 ? (windowMin / n) : 0;

          return this.staggerTargetFeeds.map((f, i) => {
            const totalMin = (startH * 60 + startM + Math.floor(i * step)) % 1440;
            const h = String(Math.floor(totalMin / 60)).padStart(2, '0');
            const m = String(totalMin % 60).padStart(2, '0');
            return { id: f.id, title: f.title || f.feed_url, time: `${h}:${m}` };
          });
        },

        get staggerAverageStep() {
          const n = this.staggerTargetFeeds.length;
          return n <= 1 ? 0 : ((Number(this.staggerForm.window_minutes) || 30) / n).toFixed(1);
        },

        async saveStaggerSchedule() {
          if (this.staggerTargetFeeds.length === 0) return;
          this.loading.stagger = true;
          try {
            await this.api('/api/admin/feeds/distribute-schedule', {
              method: 'POST',
              body: {
                feed_ids: this.staggerTargetFeeds.map(f => f.id),
                start_time: this.staggerForm.start_time || '08:00',
                window_minutes: Number(this.staggerForm.window_minutes) || 30
              }
            });
            // 乐观同步本地分配计划时刻
            const previewMap = new Map(this.staggerPreviewList.map(p => [p.id, p.time]));
            this.feeds.forEach(f => {
              if (previewMap.has(f.id)) {
                f.schedule_type = 'daily_fixed';
                f.schedule_value = previewMap.get(f.id);
              }
            });
            this.modals.stagger = false;
            this.toast('错峰时间分配已应用', 'success');
          } catch (e) {
            this.toast('错峰时间分配失败: ' + e.message, 'error');
          } finally {
            this.loading.stagger = false;
          }
        },

        // 文件夹设置 (标题与排序)
        openCategoryEditModal(group) {
          const catName = group.categoryName === '未分类' ? '' : group.categoryName;
          this.categoryForm = {
            current_name: catName,
            name: catName,
            sort_order: group.sortOrder && group.sortOrder < 999999 ? group.sortOrder : 0
          };
          this.modals.category = true;
        },

        async saveCategorySettings() {
          const newName = (this.categoryForm.name || '').trim();
          if (!newName) return this.toast('文件夹标题不能为空', 'warning');
          this.loading.category = true;
          try {
            await this.api('/api/admin/categories/update', {
              method: 'POST',
              body: {
                current_name: this.categoryForm.current_name || '',
                name: newName,
                sort_order: Number(this.categoryForm.sort_order) || 0
              }
            });
            this.modals.category = false;
            if (this.selectedCategory === (this.categoryForm.current_name || '未分类')) {
              this.selectedCategory = newName;
            }
            this.toast('文件夹设置已保存', 'success');
            await this.loadFeeds();
          } catch (e) {
            this.toast('保存文件夹设置失败: ' + e.message, 'error');
          } finally {
            this.loading.category = false;
          }
        },

        // 文件夹折叠与展开
        toggleGroup(catName) {
          this.collapsedGroups[catName] = !this.collapsedGroups[catName];
        },

        isGroupCollapsed(catName) {
          return !!this.collapsedGroups[catName];
        },

        areAllFoldersCollapsed() {
          const list = this.computedData.categoryList;
          return list.length > 0 && list.every(c => this.collapsedGroups[c.name]);
        },

        toggleAllFolders() {
          const collapse = !this.areAllFoldersCollapsed();
          this.computedData.categoryList.forEach(c => { this.collapsedGroups[c.name] = collapse; });
        },

        // 订阅表单弹窗
        openEditModal(feed) {
          this.modalForm = {
            id: feed.id,
            title: feed.title,
            feed_url: feed.feed_url,
            category_name: feed.category_name || '',
            schedule_type: feed.schedule_type,
            schedule_value: feed.schedule_value
          };
          this.modals.edit = true;
        },

        closeModal() {
          this.modals.add = false;
          this.modals.edit = false;
          this.modalForm = { id: null, title: '', feed_url: '', category_name: '', schedule_type: 'daily_fixed', schedule_value: '08:00' };
        },

        async saveModal() {
          if (!this.modalForm.feed_url) return this.toast('请输入源地址', 'warning');
          try {
            const isEdit = this.modals.edit && this.modalForm.id;
            const url = isEdit ? `/api/admin/feeds/${this.modalForm.id}` : '/api/admin/feeds';
            await this.api(url, { method: isEdit ? 'PUT' : 'POST', body: this.modalForm });
            this.closeModal();
            this.toast(isEdit ? '订阅已更新' : '订阅已添加', 'success');
            await this.loadFeeds();
          } catch (e) {
            this.toast('保存失败: ' + e.message, 'error');
          }
        },

        // 文章数据库查看
        async openDbView(feed) {
          this.dbCurrentFeed = feed;
          this.dbArticles = [];
          this.dbArticlesTotal = 0;
          this.selectedArticleIds = [];
          this.loading.db = true;
          this.modals.db = true;

          try {
            const data = await this.api(`/api/admin/feeds/${feed.id}/articles?limit=100`);
            this.dbArticles = data.articles || [];
            this.dbArticlesTotal = data.total || 0;
          } catch (e) {
            this.toast('读取数据库记录失败: ' + e.message, 'error');
          } finally {
            this.loading.db = false;
          }
        },

        // 文章多选与已读标记
        isAllArticlesSelected() {
          return this.dbArticles.length > 0 && this.dbArticles.every(a => this.selectedArticleIds.includes(a.id));
        },

        toggleSelectAllArticles() {
          this.toggleAll(this.selectedArticleIds, this.dbArticles);
        },

        async batchMarkArticlesRead(isRead) {
          if (this.selectedArticleIds.length === 0) return;
          const targetIds = [...this.selectedArticleIds];
          const idSet = new Set(targetIds);

          // 乐观更新本地 articles 状态
          const prevStates = new Map();
          let deltaUnread = 0;
          this.dbArticles.forEach(a => {
            if (idSet.has(a.id)) {
              prevStates.set(a.id, a.is_read);
              if (a.is_read !== isRead) {
                deltaUnread += isRead ? -1 : 1;
              }
              a.is_read = isRead;
            }
          });

          // 乐观同步当前 feed 和 feeds 列表的未读数，避免全量重新请求
          if (this.dbCurrentFeed && this.dbCurrentFeed.unread_count !== undefined) {
            this.dbCurrentFeed.unread_count = Math.max(0, this.dbCurrentFeed.unread_count + deltaUnread);
          }
          const feedInList = this.feeds.find(f => f.id === this.dbCurrentFeed.id);
          if (feedInList && feedInList.unread_count !== undefined) {
            feedInList.unread_count = Math.max(0, feedInList.unread_count + deltaUnread);
          }

          this.selectedArticleIds = [];
          this.toast(isRead ? `已设为已读 (${targetIds.length} 篇)` : `已设为未读 (${targetIds.length} 篇)`, 'success');

          this.loading.dbAction = true;
          try {
            await this.api('/api/admin/articles/batch-read', {
              method: 'POST',
              body: { article_ids: targetIds, is_read: isRead }
            });
          } catch (e) {
            // 出错时回滚
            this.dbArticles.forEach(a => {
              if (prevStates.has(a.id)) a.is_read = prevStates.get(a.id);
            });
            if (this.dbCurrentFeed && this.dbCurrentFeed.unread_count !== undefined) {
              this.dbCurrentFeed.unread_count = Math.max(0, this.dbCurrentFeed.unread_count - deltaUnread);
            }
            if (feedInList && feedInList.unread_count !== undefined) {
              feedInList.unread_count = Math.max(0, feedInList.unread_count - deltaUnread);
            }
            this.toast('更新已读状态失败: ' + e.message, 'error');
          } finally {
            this.loading.dbAction = false;
          }
        },

        async toggleSingleArticleRead(art) {
          const nextState = !art.is_read;
          const prevState = art.is_read;
          const delta = nextState ? -1 : 1;

          // 乐观更新
          art.is_read = nextState;
          if (this.dbCurrentFeed && this.dbCurrentFeed.unread_count !== undefined) {
            this.dbCurrentFeed.unread_count = Math.max(0, this.dbCurrentFeed.unread_count + delta);
          }
          const feedInList = this.feeds.find(f => f.id === this.dbCurrentFeed.id);
          if (feedInList && feedInList.unread_count !== undefined) {
            feedInList.unread_count = Math.max(0, feedInList.unread_count + delta);
          }

          try {
            await this.api('/api/admin/articles/batch-read', {
              method: 'POST',
              body: { article_ids: [art.id], is_read: nextState }
            });
          } catch (e) {
            // 回滚
            art.is_read = prevState;
            if (this.dbCurrentFeed && this.dbCurrentFeed.unread_count !== undefined) {
              this.dbCurrentFeed.unread_count = Math.max(0, this.dbCurrentFeed.unread_count - delta);
            }
            if (feedInList && feedInList.unread_count !== undefined) {
              feedInList.unread_count = Math.max(0, feedInList.unread_count - delta);
            }
            this.toast('切换已读状态失败: ' + e.message, 'error');
          }
        },

        // 正文查看与复制
        openContentDetail(article) {
          this.detailArticle = article;
          this.copySuccess = false;
          this.modals.content = true;
        },

        async copyRawContent() {
          if (!this.detailArticle || !this.detailArticle.content) {
            this.toast('正文内容为空', 'warning');
            return;
          }
          const text = this.detailArticle.content;
          let success = false;

          // 1. 优先使用现代 Clipboard API (仅在 HTTPS 或 localhost 安全上下文中可用)
          if (navigator.clipboard && window.isSecureContext) {
            try {
              await navigator.clipboard.writeText(text);
              success = true;
            } catch (_) {}
          }

          // 2. 降级方案：使用页面内弹窗现有的 textarea 选区执行 copy (兼容局域网 HTTP IP 访问)
          if (!success) {
            try {
              const ta = (this.$refs && this.$refs.rawContentTextarea) ? this.$refs.rawContentTextarea : document.getElementById('rawContentTextarea');
              if (ta) {
                ta.focus();
                ta.select();
                ta.setSelectionRange(0, 99999999);
                success = document.execCommand('copy');
              }
            } catch (_) {}
          }

          // 3. 兜底方案：动态创建临时 textarea 执行复制
          if (!success) {
            try {
              const temp = document.createElement('textarea');
              temp.value = text;
              temp.setAttribute('readonly', '');
              temp.style.position = 'fixed';
              temp.style.left = '-9999px';
              temp.style.top = '-9999px';
              document.body.appendChild(temp);
              temp.focus();
              temp.select();
              temp.setSelectionRange(0, 99999999);
              success = document.execCommand('copy');
              document.body.removeChild(temp);
            } catch (_) {}
          }

          if (success) {
            this.copySuccess = true;
            this.toast('正文 HTML 已复制到剪贴板', 'success');
            setTimeout(() => { this.copySuccess = false; }, 2000);
          } else {
            this.toast('复制失败，请手动全选复制', 'error');
          }
        },

        formatTime(t) {
          return t ? new Date(t).toLocaleString() : '尚未抓取';
        },

        // Token 与活跃设备管理
        async openTokensModal() {
          this.modals.tokens = true;
          await this.loadTokens();
        },

        async loadTokens() {
          this.loading.tokens = true;
          try {
            const data = await this.api('/api/admin/tokens');
            this.tokensList = Array.isArray(data) ? data : [];
          } catch (e) {
            this.toast('获取设备会话列表失败: ' + e.message, 'error');
          } finally {
            this.loading.tokens = false;
          }
        },

        async revokeOtherTokens() {
          if (!confirm('确定要注销除当前设备外的所有其他设备吗？\n其他客户端（如手机端 Reeder）将被立即踢出并需要重新登录。')) {
            return;
          }
          this.loading.tokens = true;
          try {
            await this.api('/api/admin/tokens/revoke-others', { method: 'POST' });
            this.toast('已成功注销其他所有设备会话', 'success');
            await this.loadTokens();
          } catch (e) {
            this.toast('注销失败: ' + e.message, 'error');
          } finally {
            this.loading.tokens = false;
          }
        },

        async revokeToken(id) {
          if (!confirm('确定要注销此设备会话吗？该客户端将失去访问权限。')) {
            return;
          }
          try {
            await this.api(`/api/admin/tokens/${id}`, { method: 'DELETE' });
            this.toast('设备会话已注销', 'success');
            await this.loadTokens();
          } catch (e) {
            this.toast('注销失败: ' + e.message, 'error');
          }
        },

        getClientTypeName(type) {
          if (!type) return '通用客户端';
          const lower = type.toLowerCase();
          if (lower === 'greader' || lower === 'reeder') return 'Reeder / Google Reader 客户端';
          if (lower === 'web') return 'Web 网页控制台';
          if (lower === 'legacy') return '旧版本遗留会话 (Legacy)';
          return type;
        },

        getClientIcon(type) {
          if (!type) return '🔑';
          const lower = type.toLowerCase();
          if (lower === 'web') return '💻';
          if (lower === 'greader' || lower === 'reeder') return '📱';
          if (lower === 'legacy') return '🗝️';
          return '🔑';
        }
      };
    }
