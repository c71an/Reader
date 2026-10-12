    function readerApp() {
      return {
        isLoggedIn: false,
        currentTab: localStorage.getItem('reader_tab') || 'timeline',
        feeds: [],
        collapsedGroups: {},
        selectedCategory: 'all',
        profile: { username: '', auth_token: '' },
        loginForm: { username: 'admin', password: '' },
        
        // 时间线流状态
        timelineItems: [],
        timelineContinuation: 0,
        timelineHasMore: true,
        timelineLoading: false,
        timelineFilter: 'all', // 'all' | 'unread' | 'starred'
        timelineSelectedCategory: 'all',
        readerArticle: null,
        readerIndex: -1,
        readerSwipeX: 0,
        readerClosing: false,
        timelineScrollPos: 0,
        _touchStartX: 0,
        _touchStartY: 0,
        _touchActive: false,

        // 1. 状态收纳 (统一集中管理弹窗和异步加载状态)
        menuOpen: false,
        modals: {
          add: false,
          clientInfo: false,
          tokens: false,
          db: false,
          content: false,
          stagger: false,
          category: false,
          reader: false
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
        dbEditForm: { title: '', feed_url: '', category_name: '' },
        dbArticles: [],
        dbArticlesTotal: 0,
        selectedArticleIds: [],
        selectedFeedIds: [],

        staggerTargetName: '',
        staggerTargetFeeds: [],
        staggerForm: { start_time: '08:00', interval_minutes: 30, jitter: '30m', days: [0, 1, 2, 3, 4, 5, 6] },
        categoryForm: { current_name: '', name: '', sort_order: 0 },
        modalForm: { title: '', feed_url: '', category_name: '' },

        init() {
          this.checkAuth();
          window.addEventListener('keydown', (e) => {
            if (!this.modals.reader) return;
            if (e.key === 'ArrowRight' || e.key === 'j' || e.key === 'J') {
              this.nextArticle();
            } else if (e.key === 'ArrowLeft' || e.key === 'k' || e.key === 'K') {
              this.prevArticle();
            } else if (e.key === 'Escape') {
              this.closeReader();
            }
          });
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
            this.initTimelineObserver();
            this.loadTimeline(true);
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
            if (f.schedule_type === 'cron') dailyFixedCount++;
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
                f.schedule_type = action === 'pause' ? 'paused' : 'cron';
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

        // 批量时间分配
        openStaggerModal(feeds, name) {
          if (!feeds || feeds.length === 0) return this.toast('请先选择订阅源', 'warning');
          this.staggerTargetFeeds = feeds;
          this.staggerTargetName = name || '订阅源';
          this.staggerForm = {
            start_time: '08:00',
            interval_minutes: 30,
            jitter: '30m',
            days: [0, 1, 2, 3, 4, 5, 6]
          };
          this.modals.stagger = true;
        },

        get staggerWeekExpr() {
          const days = (this.staggerForm.days || []).map(Number).sort((a, b) => a - b);
          if (days.length === 0 || days.length === 7) return '*';
          // 连续 1 到 5 为工作日简写
          if (days.length === 5 && days[0] === 1 && days[1] === 2 && days[2] === 3 && days[3] === 4 && days[4] === 5) {
            return '1-5';
          }
          return days.join(',');
        },

        get staggerPreviewList() {
          const n = this.staggerTargetFeeds.length;
          if (n === 0) return [];
          const interval = Math.max(0, Number(this.staggerForm.interval_minutes) || 0);
          const [hStr, mStr] = (this.staggerForm.start_time || '08:00').split(':');
          const startH = parseInt(hStr, 10) || 8;
          const startM = parseInt(mStr, 10) || 0;
          const week = this.staggerWeekExpr;
          const jitter = (this.staggerForm.jitter || '').trim();

          return this.staggerTargetFeeds.map((f, i) => {
            const totalMin = (startH * 60 + startM + i * interval) % 1440;
            const h = Math.floor(totalMin / 60);
            const m = totalMin % 60;
            const timeStr = `${String(h).padStart(2, '0')}:${String(m).padStart(2, '0')}`;
            let cronStr = `${m} ${h} ${week}`;
            if (jitter) {
              cronStr += ` ~${jitter}`;
            }
            return { id: f.id, title: f.title || f.feed_url, time: timeStr, cron: cronStr };
          });
        },

        async saveStaggerSchedule() {
          if (this.staggerTargetFeeds.length === 0) return;
          if (!this.staggerForm.days || this.staggerForm.days.length === 0) {
            return this.toast('请至少勾选一天生效周期', 'warning');
          }
          this.loading.stagger = true;
          try {
            await this.api('/api/admin/feeds/distribute-schedule', {
              method: 'POST',
              body: {
                feed_ids: this.staggerTargetFeeds.map(f => f.id),
                start_time: this.staggerForm.start_time || '08:00',
                interval_minutes: Math.max(0, Number(this.staggerForm.interval_minutes) || 0),
                week: this.staggerWeekExpr,
                jitter: (this.staggerForm.jitter || '').trim()
              }
            });
            // 乐观同步本地分配计划时刻
            const previewMap = new Map(this.staggerPreviewList.map(p => [p.id, p.cron]));
            this.feeds.forEach(f => {
              if (previewMap.has(f.id)) {
                f.schedule_type = 'cron';
                f.schedule_value = previewMap.get(f.id);
              }
            });
            this.modals.stagger = false;
            this.toast('时间分配已应用', 'success');
          } catch (e) {
            this.toast('时间分配失败: ' + e.message, 'error');
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

        // 添加新订阅弹窗
        closeAddModal() {
          this.modals.add = false;
          this.modalForm = { title: '', feed_url: '', category_name: '' };
        },

        async saveAddFeed() {
          if (!this.modalForm.feed_url) return this.toast('请输入源地址', 'warning');
          try {
            await this.api('/api/admin/feeds', { method: 'POST', body: this.modalForm });
            this.closeAddModal();
            this.toast('订阅已添加', 'success');
            await this.loadFeeds();
          } catch (e) {
            this.toast('添加失败: ' + e.message, 'error');
          }
        },

        // 行内调度策略快速更新 (留空为暂停)
        async updateFeedSchedule(feed, rawValue) {
          const val = (rawValue || '').trim();
          const oldType = feed.schedule_type;
          const oldValue = feed.schedule_value;

          // 1. 如果留空，则表示暂停
          if (!val) {
            if (oldType === 'paused') return;
            feed.schedule_type = 'paused';
            try {
              await this.api(`/api/admin/feeds/${feed.id}`, {
                method: 'PUT',
                body: { schedule_type: 'paused' }
              });
              this.toast(`「${feed.title || feed.feed_url}」已暂停抓取`, 'info');
            } catch (e) {
              feed.schedule_type = oldType;
              feed.schedule_value = oldValue;
              this.toast('暂停失败: ' + e.message, 'error');
              await this.loadFeeds();
            }
            return;
          }

          // 2. 如果不为空，视为 cron 调度
          if (oldType === 'cron' && oldValue === val) return;

          feed.schedule_type = 'cron';
          feed.schedule_value = val;

          try {
            await this.api(`/api/admin/feeds/${feed.id}`, {
              method: 'PUT',
              body: { schedule_type: 'cron', schedule_value: val }
            });
            this.toast(`「${feed.title || feed.feed_url}」调度已更新为: ${val}`, 'success');
          } catch (e) {
            feed.schedule_type = oldType;
            feed.schedule_value = oldValue;
            this.toast('更新调度失败: ' + e.message, 'error');
            await this.loadFeeds();
          }
        },

        // 文章与订阅源信息查看/编辑 (合并窗口)
        async openDbView(feed) {
          this.dbCurrentFeed = feed;
          this.dbEditForm = {
            title: feed.title || '',
            feed_url: feed.feed_url || '',
            category_name: feed.category_name || ''
          };
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

        isDbFeedModified() {
          if (!this.dbCurrentFeed) return false;
          return (this.dbEditForm.title !== (this.dbCurrentFeed.title || '')) ||
                 (this.dbEditForm.feed_url !== (this.dbCurrentFeed.feed_url || '')) ||
                 (this.dbEditForm.category_name !== (this.dbCurrentFeed.category_name || ''));
        },

        async saveFeedSettingsInDbView() {
          if (!this.dbCurrentFeed) return;
          if (!this.dbEditForm.feed_url) return this.toast('源地址不能为空', 'warning');
          try {
            await this.api(`/api/admin/feeds/${this.dbCurrentFeed.id}`, {
              method: 'PUT',
              body: {
                title: this.dbEditForm.title,
                feed_url: this.dbEditForm.feed_url,
                category_name: this.dbEditForm.category_name
              }
            });
            this.dbCurrentFeed.title = this.dbEditForm.title;
            this.dbCurrentFeed.feed_url = this.dbEditForm.feed_url;
            this.dbCurrentFeed.category_name = this.dbEditForm.category_name;
            this.toast('订阅源配置已保存', 'success');
            await this.loadFeeds();
          } catch (e) {
            this.toast('保存配置失败: ' + e.message, 'error');
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

        async copyText(text, successMsg = '已复制到剪贴板') {
          if (!text) return;
          let success = false;

          // 1. 优先使用现代 Clipboard API (仅在 HTTPS 或 localhost 安全上下文中可用)
          if (navigator.clipboard && window.isSecureContext) {
            try {
              await navigator.clipboard.writeText(text);
              success = true;
            } catch (_) {}
          }

          // 2. 降级方案：创建临时 textarea 执行复制 (兼容局域网 HTTP IP 访问)
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
            this.toast(successMsg, 'success');
          } else {
            this.toast('复制失败，请手动复制', 'error');
          }
          return success;
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
          const success = await this.copyText(this.detailArticle.content, '正文 HTML 已复制到剪贴板');
          if (success) {
            this.copySuccess = true;
            setTimeout(() => { this.copySuccess = false; }, 2000);
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
        },

        // ==================== 时间线流核心逻辑 ====================

        get timelineGroups() {
          const dateMap = new Map();
          for (const item of this.timelineItems) {
            const dateHeader = this.formatDateHeader(item.published_at);
            if (!dateMap.has(dateHeader)) {
              dateMap.set(dateHeader, {
                date: dateHeader,
                rawDate: new Date(item.published_at).getTime() || 0,
                items: []
              });
            }
            dateMap.get(dateHeader).items.push(item);
          }

          const groups = Array.from(dateMap.values());
          // 严格按日期从最新到最早降序排列
          groups.sort((a, b) => b.rawDate - a.rawDate);

          // 组内文章也按发布时间倒序排列
          for (const group of groups) {
            group.items.sort((a, b) => (new Date(b.published_at).getTime() || 0) - (new Date(a.published_at).getTime() || 0));
          }

          return groups;
        },

        formatDateHeader(dateStr) {
          if (!dateStr) return 'EARLIER';
          const d = new Date(dateStr);
          if (isNaN(d.getTime())) return 'EARLIER';

          const dayNames = ['SUNDAY', 'MONDAY', 'TUESDAY', 'WEDNESDAY', 'THURSDAY', 'FRIDAY', 'SATURDAY'];
          const monthNames = ['JANUARY', 'FEBRUARY', 'MARCH', 'APRIL', 'MAY', 'JUNE', 'JULY', 'AUGUST', 'SEPTEMBER', 'OCTOBER', 'NOVEMBER', 'DECEMBER'];

          return `${dayNames[d.getDay()]}, ${monthNames[d.getMonth()]} ${d.getDate()}, ${d.getFullYear()}`;
        },

        formatItemTime(dateStr) {
          if (!dateStr) return '';
          const d = new Date(dateStr);
          if (isNaN(d.getTime())) return '';
          const h = String(d.getHours()).padStart(2, '0');
          const m = String(d.getMinutes()).padStart(2, '0');
          return `${h}:${m}`;
        },

        formatReadableTime(dateStr) {
          if (!dateStr) return '';
          const d = new Date(dateStr);
          if (isNaN(d.getTime())) return dateStr;
          return d.toLocaleString('zh-CN', { hour12: false });
        },

        processTimelineItem(item) {
          const content = item.content || '';
          let thumbnail = '';
          let isLive = false;

          // 识别 Live 图：包含 [Live Photo] 关键词、video 标签或视频扩展名
          if (content.includes('[Live Photo]') || content.includes('<video') || content.includes('.mp4') || content.includes('.mov')) {
            isLive = true;
          }

          // 提取首图作为右侧缩略图：过滤掉表情包、头像与透明占位图，确保选出真正的正文配图
          const isEmoticonOrAvatar = (src) => {
            if (!src) return true;
            const s = src.toLowerCase();
            return s.includes('/emoticon/') ||
                   s.includes('sinaimg.cn/m/emoticon') ||
                   s.includes('weibo.cn/emoticon') ||
                   s.includes('/emote/') ||
                   s.includes('/emoji') ||
                   s.includes('emoji') ||
                   s.includes('/avatar/') ||
                   s.includes('data:image/gif;base64,r0lgodlhaqab') ||
                   s.includes('1x1');
          };

          // URL 相对路径自动补全辅助函数
          const resolveUrl = (rawSrc, baseUrl) => {
            if (!rawSrc) return '';
            const trimmed = rawSrc.trim();
            if (trimmed.startsWith('http://') || trimmed.startsWith('https://') || trimmed.startsWith('data:') || trimmed.startsWith('//')) {
              if (trimmed.startsWith('//')) return 'https:' + trimmed;
              return trimmed;
            }
            if (!baseUrl) return trimmed;
            try {
              return new URL(trimmed, baseUrl).href;
            } catch (_) {
              return trimmed;
            }
          };

          const imgRegex = /<img[^>]+src=["']([^"']+)["'][^>]*>/gi;
          let match;
          let firstFallbackImg = '';
          while ((match = imgRegex.exec(content)) !== null) {
            const rawSrc = match[1];
            const fullSrc = resolveUrl(rawSrc, item.url);
            if (!firstFallbackImg) firstFallbackImg = fullSrc;
            if (!isEmoticonOrAvatar(rawSrc)) {
              thumbnail = fullSrc;
              break;
            }
          }

          // 若未匹配到有效正文图片，检查视频封面 (Weibo Live Photo poster 等)
          if (!thumbnail) {
            const posterMatch = content.match(/<video[^>]+poster=["']([^"']+)["']/i);
            if (posterMatch && posterMatch[1] && !isEmoticonOrAvatar(posterMatch[1])) {
              thumbnail = resolveUrl(posterMatch[1], item.url);
            } else if (firstFallbackImg) {
              thumbnail = firstFallbackImg;
            }
          }

          // 提取纯文本摘要
          let text = content.replace(/<style[^>]*>[\s\S]*?<\/style>/gi, '')
                            .replace(/<script[^>]*>[\s\S]*?<\/script>/gi, '')
                            .replace(/<[^>]+>/g, ' ')
                            .replace(/&nbsp;/g, ' ')
                            .replace(/&amp;/g, '&')
                            .replace(/&lt;/g, '<')
                            .replace(/&gt;/g, '>')
                            .replace(/&quot;/g, '"')
                            .replace(/&#39;/g, "'")
                            .replace(/\s+/g, ' ')
                            .trim();

          item._thumbnail = thumbnail;
          item._isLive = isLive;
          item._snippet = text.slice(0, 180);
          return item;
        },

        formatReaderHtml(content) {
          if (!content) return '<p class="text-slate-400">（无正文内容）</p>';
          let html = content;

          // 1. 过滤废弃的旧 HTML 行内属性: bgcolor, color, background
          html = html.replace(/\s+(bgcolor|color)\s*=\s*(["'][^"']*["']|[^\s>]+)/gi, '');

          // 2. 清洗内联 style 属性：丢弃字号大小(font-size)、颜色(color)、背景(background/background-color/image)、字体族(font-family)
          html = html.replace(/\s+style=(["'])([\s\S]*?)\1/gi, (match, quote, styleContent) => {
            const decls = styleContent.split(';');
            const filtered = decls.filter(d => {
              const trimmed = d.trim().toLowerCase();
              if (!trimmed) return false;
              const prop = trimmed.split(':')[0].trim();
              return !['color', 'background', 'background-color', 'background-image', 'font-size', 'font-family'].includes(prop);
            });
            if (filtered.length === 0) return '';
            return ' style=' + quote + filtered.join('; ').trim() + quote;
          });

          // 3. 清洗 <img> 标签上的硬编码 style 中的 width/height 以及固定宽高属性，并自动补全相对路径
          const articleBaseUrl = this.readerArticle ? this.readerArticle.url : '';
          html = html.replace(/<img\b([^>]*?)>/gi, (match, attrs) => {
            // 清洗 style 里的 width/height
            let newAttrs = attrs.replace(/\s+style=(["'])([\s\S]*?)\1/gi, (sMatch, quote, styleContent) => {
              const decls = styleContent.split(';');
              const filtered = decls.filter(d => {
                const prop = d.trim().toLowerCase().split(':')[0].trim();
                return prop !== 'width' && prop !== 'height' && prop !== 'max-width' && prop !== 'max-height';
              });
              if (filtered.length === 0) return '';
              return ' style=' + quote + filtered.join('; ').trim() + quote;
            });

            // 补全相对路径
            if (articleBaseUrl) {
              newAttrs = newAttrs.replace(/\s+src=(["'])([\s\S]*?)\1/gi, (srcMatch, quote, srcVal) => {
                const trimmed = srcVal.trim();
                if (trimmed && !trimmed.startsWith('http://') && !trimmed.startsWith('https://') && !trimmed.startsWith('data:') && !trimmed.startsWith('//')) {
                  try {
                    const resolved = new URL(trimmed, articleBaseUrl).href;
                    return ` src=${quote}${resolved}${quote}`;
                  } catch (_) {}
                }
                return srcMatch;
              });
            }

            if (!newAttrs.includes('referrerpolicy')) {
              newAttrs += ' referrerpolicy="no-referrer"';
            }
            if (!newAttrs.includes('loading')) {
              newAttrs += ' loading="lazy"';
            }
            return `<img ${newAttrs.trim()}>`;
          });

          // 4. 为所有视频自动注入 playsinline 与 controls，防止移动端打断全屏
          html = html.replace(/<video\b([^>]*?)>/gi, (match, attrs) => {
            if (!attrs.includes('playsinline')) {
              attrs += ' playsinline webkit-playsinline';
            }
            if (!attrs.includes('controls')) {
              attrs += ' controls';
            }
            return `<video ${attrs}>`;
          });

          return html;
        },

        setTab(tab) {
          this.currentTab = tab;
          localStorage.setItem('reader_tab', tab);
          if (tab === 'timeline') {
            if (this.timelineItems.length === 0) {
              this.loadTimeline(true);
            }
            this.initTimelineObserver();
          }
        },

        setTimelineFilter(filter) {
          this.timelineFilter = filter;
          this.loadTimeline(true);
        },

        async loadTimeline(reset = false) {
          if (this.timelineLoading) return;
          if (!reset && !this.timelineHasMore) return;

          if (reset) {
            this.timelineContinuation = 0;
            this.timelineHasMore = true;
            this.timelineItems = [];
          }

          this.timelineLoading = true;
          try {
            const params = new URLSearchParams();
            params.set('limit', '30');
            if (this.timelineContinuation) {
              params.set('continuation', String(this.timelineContinuation));
            }

            if (this.timelineFilter === 'unread') {
              params.set('unread_only', 'true');
            } else if (this.timelineFilter === 'starred') {
              params.set('starred_only', 'true');
            }

            if (this.timelineSelectedCategory !== 'all') {
              params.set('stream_id', `user/-/label/${this.timelineSelectedCategory}`);
            } else {
              params.set('stream_id', 'user/-/state/com.google/reading-list');
            }

            const data = await this.api(`/api/admin/timeline?${params.toString()}`);
            const newItems = (data.items || []).map(it => this.processTimelineItem(it));

            if (reset) {
              this.timelineItems = newItems;
            } else {
              this.timelineItems.push(...newItems);
            }

            if (data.continuation && newItems.length >= 30) {
              this.timelineContinuation = data.continuation;
              this.timelineHasMore = true;
            } else {
              this.timelineContinuation = 0;
              this.timelineHasMore = false;
            }
          } catch (e) {
            this.toast('加载时间线失败: ' + e.message, 'error');
          } finally {
            this.timelineLoading = false;
          }
        },

        initTimelineObserver() {
          setTimeout(() => {
            const sentinel = document.getElementById('timelineSentinel');
            if (!sentinel || sentinel._observed) return;
            sentinel._observed = true;
            const observer = new IntersectionObserver((entries) => {
              if (entries[0].isIntersecting && !this.timelineLoading && this.timelineHasMore && this.currentTab === 'timeline') {
                this.loadTimeline(false);
              }
            }, { rootMargin: '300px' });
            observer.observe(sentinel);
          }, 100);
        },

        openReader(article) {
          // 记录当前列表滚动位置，切换到文章页面流
          this.timelineScrollPos = window.scrollY || document.documentElement.scrollTop || 0;
          this.readerArticle = article;
          this.readerIndex = this.timelineItems.findIndex(it => it.id === article.id);
          this.readerSwipeX = 0;
          this.readerClosing = false;
          this.modals.reader = true;

          // 平滑重置到页面顶端并聚焦
          this.$nextTick(() => {
            window.scrollTo({ top: 0, behavior: 'instant' });
          });

          // 自动标记已读
          if (!article.is_read) {
            article.is_read = true;
            this.api('/api/admin/articles/batch-read', {
              method: 'POST',
              body: { article_ids: [article.id], is_read: true }
            }).catch(() => {});

            // 同步订阅源未读统计
            const f = this.feeds.find(feed => feed.id === article.feed_id);
            if (f && f.unread_count > 0) f.unread_count--;
          }
        },

        closeReader() {
          if (this.readerClosing) return;
          this.readerClosing = true;
          const targetPos = this.timelineScrollPos || 0;
          const activeArticleId = this.readerArticle ? this.readerArticle.id : null;

          setTimeout(() => {
            this.modals.reader = false;
            this.readerArticle = null;
            this.readerIndex = -1;
            this.readerSwipeX = 0;
            this.readerClosing = false;

            // 无缝恢复列表位置：优先将刚阅读完毕的卡片平滑定位至视口舒适区 (避开底栏遮挡危险区)
            this.$nextTick(() => {
              if (activeArticleId) {
                const cardEl = document.getElementById('article-card-' + activeArticleId);
                if (cardEl) {
                  // 计算卡片距离顶部的偏移，预留约 80px 顶栏空间，让卡片舒适地停留在视口上部 15%~25%
                  const cardTop = cardEl.getBoundingClientRect().top + window.scrollY;
                  const idealScrollY = Math.max(0, cardTop - 80);
                  window.scrollTo({ top: idealScrollY, behavior: 'instant' });
                  return;
                }
              }
              window.scrollTo({ top: targetPos, behavior: 'instant' });
            });
          }, 180);
        },

        // 文章详情页滑动返回手势 (左向右滑)
        handleReaderTouchStart(e) {
          if (!e.touches || e.touches.length !== 1) return;
          const touch = e.touches[0];
          this._touchStartX = touch.clientX;
          this._touchStartY = touch.clientY;
          this._touchActive = true;
          this.readerSwipeX = 0;
        },

        handleReaderTouchMove(e) {
          if (!this._touchActive || !e.touches || e.touches.length !== 1) return;
          const touch = e.touches[0];
          const dx = touch.clientX - this._touchStartX;
          const dy = touch.clientY - this._touchStartY;

          // 仅当向右滑动且水平位移明显大于垂直位移时触发滑动返回视觉反馈
          if (dx > 10 && Math.abs(dx) > Math.abs(dy) * 1.2) {
            if (e.cancelable) {
              e.preventDefault();
            }
            this.readerSwipeX = Math.min(window.innerWidth || 500, Math.max(0, dx));
          } else if (dx < 0) {
            this.readerSwipeX = 0;
          }
        },

        handleReaderTouchEnd(e) {
          if (!this._touchActive) return;
          this._touchActive = false;
          // 若右滑超过 85px，执行平滑关闭；否则回弹归位
          if (this.readerSwipeX > 85) {
            this.closeReader();
          } else {
            this.readerSwipeX = 0;
          }
        },

        prevArticle() {
          if (this.readerIndex > 0) {
            this.openReader(this.timelineItems[this.readerIndex - 1]);
          }
        },

        nextArticle() {
          if (this.readerIndex < this.timelineItems.length - 1) {
            this.openReader(this.timelineItems[this.readerIndex + 1]);
          } else if (this.timelineHasMore && !this.timelineLoading) {
            this.loadTimeline(false).then(() => {
              if (this.readerIndex < this.timelineItems.length - 1) {
                this.openReader(this.timelineItems[this.readerIndex + 1]);
              }
            });
          }
        },

        async toggleReaderArticleStar() {
          if (!this.readerArticle) return;
          const nextStarred = !this.readerArticle.is_starred;
          this.readerArticle.is_starred = nextStarred;
          try {
            await this.api('/api/admin/articles/batch-star', {
              method: 'POST',
              body: { article_ids: [this.readerArticle.id], is_starred: nextStarred }
            });
            this.toast(nextStarred ? '已添加标星收藏' : '已取消标星', 'success');
          } catch (e) {
            this.readerArticle.is_starred = !nextStarred;
            this.toast('操作失败: ' + e.message, 'error');
          }
        },

        async toggleReaderArticleRead() {
          if (!this.readerArticle) return;
          const nextRead = !this.readerArticle.is_read;
          this.readerArticle.is_read = nextRead;
          try {
            await this.api('/api/admin/articles/batch-read', {
              method: 'POST',
              body: { article_ids: [this.readerArticle.id], is_read: nextRead }
            });
            const f = this.feeds.find(feed => feed.id === this.readerArticle.feed_id);
            if (f && f.unread_count !== undefined) {
              f.unread_count = Math.max(0, f.unread_count + (nextRead ? -1 : 1));
            }
            this.toast(nextRead ? '已设为已读' : '已恢复为未读', 'success');
          } catch (e) {
            this.readerArticle.is_read = !nextRead;
            this.toast('操作失败: ' + e.message, 'error');
          }
        },

        async markAllTimelineRead() {
          if (!confirm('确定要将当前时间线的所有文章标记为已读吗？')) {
            return;
          }
          let streamId = 'user/-/state/com.google/reading-list';
          if (this.timelineSelectedCategory !== 'all') {
            streamId = `user/-/label/${this.timelineSelectedCategory}`;
          }
          try {
            await this.api('/api/admin/articles/mark-all-read', {
              method: 'POST',
              body: { stream_id: streamId }
            });
            this.timelineItems.forEach(it => { it.is_read = true; });
            await this.loadFeeds();
            this.toast('已全部标记为已读', 'success');
            if (this.timelineFilter === 'unread') {
              this.loadTimeline(true);
            }
          } catch (e) {
            this.toast('操作失败: ' + e.message, 'error');
          }
        }
      };
    }
