import 'dart:async';
import 'dart:developer' as developer;
import 'package:flutter/material.dart';
import '../services/nz_config.dart';
import '../services/nz_service.dart';
import '../services/nz_ls_service.dart';
import 'setup_screen.dart';
import 'settings_screen.dart';

class HomeScreen extends StatefulWidget {
  const HomeScreen({super.key});

  @override
  State<HomeScreen> createState() => _HomeScreenState();
}

class _HomeScreenState extends State<HomeScreen> with SingleTickerProviderStateMixin {
  NzConfig? _config;
  bool _connected = false;
  bool _loading = true;
  bool _connecting = false;
  bool _disconnecting = false;
  String _routeMode = 'auto';
  bool _showingNodeList = false;
  Timer? _statusTimer;

  late AnimationController _pulseController;
  late Animation<double> _pulseAnimation;

  @override
  void initState() {
    super.initState();
    _pulseController = AnimationController(
      vsync: this,
      duration: const Duration(seconds: 2),
    )..repeat(reverse: true);
    _pulseAnimation = Tween<double>(begin: 0.8, end: 1.0).animate(
      CurvedAnimation(parent: _pulseController, curve: Curves.easeInOut),
    );
    _init();
  }

  @override
  void dispose() {
    _statusTimer?.cancel();
    _pulseController.dispose();
    super.dispose();
  }

  Future<void> _init() async {
    final config = await NzConfig.load();
    setState(() {
      _config = config;
      _connected = false;
      _loading = false;
    });

    if (config == null && mounted) {
      _navigateToSetup();
    }

    _statusTimer = Timer.periodic(const Duration(seconds: 2), (_) {
      _checkStatus();
    });
  }

  Future<void> _checkStatus() async {
    final running = await NzService.isRunning();
    if (mounted) {
      setState(() {
        _connected = running;
        if (running) {
          _connecting = false;
        } else {
          _disconnecting = false;
        }
      });
    }
  }

  Future<void> _updateRouteMode() async {
  }

  void _navigateToSetup() async {
    final result = await Navigator.push<NzConfig>(
      context,
      MaterialPageRoute(builder: (_) => const SetupScreen()),
    );
    if (result != null) {
      setState(() => _config = result);
    }
  }

  void _navigateToSettings() async {
    if (_config == null) return;
    final result = await Navigator.push<NzConfig>(
      context,
      MaterialPageRoute(builder: (_) => SettingsScreen(config: _config!)),
    );
    if (result != null) {
      setState(() => _config = result);
    }
  }

  Future<void> _toggleConnection(bool value) async {
    if (_config == null || !_config!.isValid) return;

    setState(() => _loading = true);

    bool success;
    if (value) {
      success = await NzService.start(_config!.toJsonString());
    } else {
      success = await NzService.stop();
    }
    if (success) {
      setState(() {
        _loading = false;
        if (value) {
          _connecting = true;
        } else {
          _disconnecting = true;
        }
      });
      if (value) {
        _updateRouteMode();
      }
    } else {
      setState(() => _loading = false);
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(value ? 'Failed to connect' : 'Failed to disconnect'),
            behavior: SnackBarBehavior.floating,
          ),
        );
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    if (_loading && _config == null) {
      return const Scaffold(
        body: Center(child: CircularProgressIndicator()),
      );
    }

    if (_config == null) {
      return const Scaffold(
        body: Center(child: CircularProgressIndicator()),
      );
    }

    final theme = Theme.of(context);
    final isDark = theme.brightness == Brightness.dark;

    return Scaffold(
      appBar: AppBar(
        title: const Text('nz'),
        actions: [
          IconButton(
            icon: const Icon(Icons.settings_outlined),
            onPressed: _navigateToSettings,
          ),
        ],
      ),
      body: SafeArea(
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 24),
          child: Column(
            children: [
              const SizedBox(height: 48),
              _buildStatusIndicator(theme, isDark),
              const SizedBox(height: 32),
              _buildStatusText(theme),
              const SizedBox(height: 8),
              _buildSubStatusText(theme),
              const SizedBox(height: 48),
              _buildConnectionCard(theme, isDark),
              const Spacer(),
              _buildInfoSection(theme),
              const SizedBox(height: 32),
            ],
          ),
        ),
      ),
    );
  }

  Widget _buildStatusIndicator(ThemeData theme, bool isDark) {
    final color = _disconnecting
        ? const Color(0xFFFF9500)
        : _connecting
            ? const Color(0xFFFF9500)
            : _connected
                ? const Color(0xFF34C759)
                : Colors.grey.shade400;

    return AnimatedBuilder(
      animation: _pulseAnimation,
      builder: (context, child) {
        return Container(
          width: 120,
          height: 120,
          decoration: BoxDecoration(
            shape: BoxShape.circle,
            color: color.withOpacity(0.1),
          ),
          child: Center(
            child: _connecting || _disconnecting
                ? SizedBox(
                    width: 80,
                    height: 80,
                    child: CircularProgressIndicator(
                      color: color,
                      strokeWidth: 3,
                    ),
                  )
                : Container(
                    width: 80,
                    height: 80,
                    decoration: BoxDecoration(
                      shape: BoxShape.circle,
                      color: color.withOpacity(_connected ? _pulseAnimation.value : 0.3),
                      boxShadow: _connected
                          ? [
                              BoxShadow(
                                color: color.withOpacity(0.3),
                                blurRadius: 20,
                                spreadRadius: 5,
                              ),
                            ]
                          : null,
                    ),
                    child: Icon(
                      _connected ? Icons.lock_outlined : Icons.lock_open_outlined,
                      size: 36,
                      color: Colors.white,
                    ),
                  ),
          ),
        );
      },
    );
  }

  Widget _buildStatusText(ThemeData theme) {
    return Text(
      _disconnecting
          ? 'Disconnecting...'
          : _connecting
              ? 'Connecting...'
              : _connected
                  ? 'Connected'
                  : 'Disconnected',
      style: theme.textTheme.headlineSmall?.copyWith(
        fontWeight: FontWeight.w600,
        color: _disconnecting
            ? const Color(0xFFFF9500)
            : _connecting
                ? const Color(0xFFFF9500)
                : _connected
                    ? const Color(0xFF34C759)
                    : null,
      ),
    );
  }

  Widget _buildSubStatusText(ThemeData theme) {
    return Text(
      _connected
          ? '192.168.100.${_config!.ip}'
          : _connecting
              ? 'Connecting to ${_config!.domain}...'
              : _disconnecting
                  ? 'Disconnecting from ${_config!.domain}...'
                  : _config!.domain,
      style: theme.textTheme.bodyMedium?.copyWith(
        color: theme.textTheme.bodyMedium?.color?.withOpacity(0.6),
      ),
    );
  }

  Widget _buildConnectionCard(ThemeData theme, bool isDark) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(20),
      decoration: BoxDecoration(
        color: isDark ? const Color(0xFF2C2C2E) : Colors.white,
        borderRadius: BorderRadius.circular(16),
        boxShadow: [
          BoxShadow(
            color: Colors.black.withOpacity(isDark ? 0.3 : 0.05),
            blurRadius: 10,
            offset: const Offset(0, 2),
          ),
        ],
      ),
      child: Row(
        children: [
          Container(
            width: 48,
            height: 48,
            decoration: BoxDecoration(
              color: const Color(0xFF6C63FF).withOpacity(0.1),
              borderRadius: BorderRadius.circular(12),
            ),
            child: const Icon(
              Icons.vpn_key_outlined,
              color: Color(0xFF6C63FF),
            ),
          ),
          const SizedBox(width: 16),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  _config!.name,
                  style: theme.textTheme.titleMedium?.copyWith(
                    fontWeight: FontWeight.w600,
                  ),
                ),
                const SizedBox(height: 4),
                Text(
                  _config!.domain,
                  style: theme.textTheme.bodySmall?.copyWith(
                    color: theme.textTheme.bodySmall?.color?.withOpacity(0.6),
                  ),
                ),
              ],
            ),
          ),
          Switch.adaptive(
            value: _connected || _connecting || _disconnecting,
            onChanged: _loading ? null : _toggleConnection,
            activeColor: _disconnecting
                ? const Color(0xFFFF9500)
                : _connecting
                    ? const Color(0xFFFF9500)
                    : const Color(0xFF34C759),
          ),
        ],
      ),
    );
  }

  Widget _buildInfoSection(ThemeData theme) {
    return Column(
      children: [
        _buildInfoRow(theme, 'Network', '192.168.100.0/24'),
        const SizedBox(height: 12),
        _buildInfoRow(theme, 'Server', _config!.domain),
        const SizedBox(height: 12),
        _buildInfoRow(theme, 'Route', _connected ? _routeMode : 'auto'),
        const SizedBox(height: 16),
        SizedBox(
          width: double.infinity,
          child: OutlinedButton.icon(
            onPressed: _connected ? _showNodeList : null,
            icon: const Icon(Icons.list_alt),
            label: const Text('View Nodes'),
            style: OutlinedButton.styleFrom(
              padding: const EdgeInsets.symmetric(vertical: 12),
            ),
          ),
        ),
      ],
    );
  }

  Widget _buildInfoRow(ThemeData theme, String label, String value) {
    return Row(
      mainAxisAlignment: MainAxisAlignment.spaceBetween,
      children: [
        Text(
          label,
          style: theme.textTheme.bodyMedium?.copyWith(
            color: theme.textTheme.bodyMedium?.color?.withOpacity(0.5),
          ),
        ),
        Text(
          value,
          style: theme.textTheme.bodyMedium?.copyWith(
            fontWeight: FontWeight.w500,
          ),
        ),
      ],
    );
  }

  void _showNodeList() async {
    if (_showingNodeList) return;
    _showingNodeList = true;

    final nodes = await NzLsService.getNodeList();
    if (!mounted) {
      _showingNodeList = false;
      return;
    }

    showDialog(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Nodes'),
        content: SizedBox(
          width: double.maxFinite,
          child: SingleChildScrollView(
            scrollDirection: Axis.horizontal,
            child: SingleChildScrollView(
              child: _buildNodeTable(nodes),
            ),
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context),
            child: const Text('Close'),
          ),
        ],
      ),
    ).then((_) {
      _showingNodeList = false;
    });
  }

  Widget _buildNodeTable(List<NzNodeInfo> nodes) {
    return DataTable(
      columns: const [
        DataColumn(label: Text('Name')),
        DataColumn(label: Text('IP')),
      ],
      rows: nodes.map((node) {
        final isLocal = node.name == _config?.name;
        final Color ipColor;
        if (isLocal) {
          ipColor = Colors.blue;
        } else if (node.status == 'online') {
          ipColor = Colors.green;
        } else {
          ipColor = Colors.red;
        }

        return DataRow(
          cells: [
            DataCell(Text(
              node.name,
              style: TextStyle(
                fontWeight: isLocal ? FontWeight.bold : FontWeight.normal,
              ),
            )),
            DataCell(Text(
              node.ip,
              style: TextStyle(color: ipColor),
            )),
          ],
        );
      }).toList(),
    );
  }
}
