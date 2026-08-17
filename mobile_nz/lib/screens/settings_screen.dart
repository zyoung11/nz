import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import '../services/nz_config.dart';

class SettingsScreen extends StatefulWidget {
  final NzConfig config;

  const SettingsScreen({super.key, required this.config});

  @override
  State<SettingsScreen> createState() => _SettingsScreenState();
}

class _SettingsScreenState extends State<SettingsScreen> {
  final _formKey = GlobalKey<FormState>();
  late final TextEditingController _nameController;
  late final TextEditingController _passwordController;
  late final TextEditingController _domainController;
  late final TextEditingController _ipController;
  bool _loading = false;
  bool _obscurePassword = true;
  bool _changed = false;

  @override
  void initState() {
    super.initState();
    _nameController = TextEditingController(text: widget.config.name);
    _passwordController = TextEditingController(text: widget.config.password);
    _domainController = TextEditingController(text: widget.config.domain);
    _ipController = TextEditingController(text: widget.config.ip.toString());
  }

  @override
  void dispose() {
    _nameController.dispose();
    _passwordController.dispose();
    _domainController.dispose();
    _ipController.dispose();
    super.dispose();
  }

  void _markChanged() {
    if (!_changed) {
      setState(() => _changed = true);
    }
  }

  Future<void> _save() async {
    if (!_formKey.currentState!.validate()) return;

    setState(() => _loading = true);

    final config = NzConfig(
      name: _nameController.text.trim(),
      password: _passwordController.text,
      domain: _domainController.text.trim(),
      ip: int.parse(_ipController.text),
    );

    await config.save();

    setState(() {
      _loading = false;
      _changed = false;
    });

    if (mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text('Configuration saved'),
          behavior: SnackBarBehavior.floating,
        ),
      );
      Navigator.pop(context, config);
    }
  }

  Future<void> _reset() async {
    final confirm = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Reset Configuration'),
        content: const Text('This will delete all saved settings. Are you sure?'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('Cancel'),
          ),
          TextButton(
            onPressed: () => Navigator.pop(context, true),
            style: TextButton.styleFrom(foregroundColor: Colors.red),
            child: const Text('Reset'),
          ),
        ],
      ),
    );

    if (confirm == true) {
      await widget.config.delete();
      if (mounted) {
        Navigator.pop(context, null);
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);

    return Scaffold(
      appBar: AppBar(
        title: const Text('Settings'),
        actions: [
          if (_changed)
            TextButton(
              onPressed: _loading ? null : _save,
              child: const Text('Save'),
            ),
        ],
      ),
      body: SafeArea(
        child: SingleChildScrollView(
          padding: const EdgeInsets.symmetric(horizontal: 24),
          child: Form(
            key: _formKey,
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                const SizedBox(height: 24),
                _buildSection(theme, 'Node Configuration', [
                  _buildNameField(),
                  const SizedBox(height: 16),
                  _buildPasswordField(),
                  const SizedBox(height: 16),
                  _buildDomainField(),
                  const SizedBox(height: 16),
                  _buildIpField(),
                ]),
                const SizedBox(height: 32),
                _buildSection(theme, 'Network Info', [
                  _buildInfoRow(theme, 'Network', '192.168.100.0/24'),
                  const Divider(height: 1),
                  _buildInfoRow(theme, 'VPN IP', '192.168.100.${widget.config.ip}'),
                  const Divider(height: 1),
                  _buildInfoRow(theme, 'Route', 'auto'),
                ]),
                const SizedBox(height: 32),
                if (_changed) ...[
                  SizedBox(
                    width: double.infinity,
                    height: 52,
                    child: ElevatedButton(
                      onPressed: _loading ? null : _save,
                      style: ElevatedButton.styleFrom(
                        backgroundColor: const Color(0xFF6C63FF),
                        foregroundColor: Colors.white,
                        shape: RoundedRectangleBorder(
                          borderRadius: BorderRadius.circular(12),
                        ),
                        elevation: 0,
                      ),
                      child: _loading
                          ? const SizedBox(
                              width: 24,
                              height: 24,
                              child: CircularProgressIndicator(
                                strokeWidth: 2,
                                color: Colors.white,
                              ),
                            )
                          : const Text(
                              'Save Changes',
                              style: TextStyle(
                                fontSize: 16,
                                fontWeight: FontWeight.w600,
                              ),
                            ),
                    ),
                  ),
                  const SizedBox(height: 16),
                ],
                SizedBox(
                  width: double.infinity,
                  height: 52,
                  child: OutlinedButton(
                    onPressed: _reset,
                    style: OutlinedButton.styleFrom(
                      foregroundColor: Colors.red,
                      side: const BorderSide(color: Colors.red),
                      shape: RoundedRectangleBorder(
                        borderRadius: BorderRadius.circular(12),
                      ),
                    ),
                    child: const Text(
                      'Reset Configuration',
                      style: TextStyle(
                        fontSize: 16,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                  ),
                ),
                const SizedBox(height: 32),
              ],
            ),
          ),
        ),
      ),
    );
  }

  Widget _buildSection(ThemeData theme, String title, List<Widget> children) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          title,
          style: theme.textTheme.titleSmall?.copyWith(
            fontWeight: FontWeight.w600,
            color: theme.textTheme.titleSmall?.color?.withOpacity(0.5),
          ),
        ),
        const SizedBox(height: 12),
        Container(
          width: double.infinity,
          padding: const EdgeInsets.all(16),
          decoration: BoxDecoration(
            color: theme.brightness == Brightness.dark
                ? const Color(0xFF2C2C2E)
                : Colors.white,
            borderRadius: BorderRadius.circular(12),
            boxShadow: [
              BoxShadow(
                color: Colors.black.withOpacity(
                  theme.brightness == Brightness.dark ? 0.3 : 0.05,
                ),
                blurRadius: 10,
                offset: const Offset(0, 2),
              ),
            ],
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: children,
          ),
        ),
      ],
    );
  }

  Widget _buildNameField() {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          'Node Name',
          style: Theme.of(context).textTheme.titleSmall?.copyWith(
                fontWeight: FontWeight.w600,
              ),
        ),
        const SizedBox(height: 8),
        TextFormField(
          controller: _nameController,
          decoration: const InputDecoration(
            hintText: 'e.g. my-phone',
            prefixIcon: Icon(Icons.smartphone_outlined),
          ),
          textInputAction: TextInputAction.next,
          onChanged: (_) => _markChanged(),
          validator: (value) {
            if (value == null || value.trim().isEmpty) {
              return 'Name is required';
            }
            return null;
          },
        ),
      ],
    );
  }

  Widget _buildPasswordField() {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          'Password',
          style: Theme.of(context).textTheme.titleSmall?.copyWith(
                fontWeight: FontWeight.w600,
              ),
        ),
        const SizedBox(height: 8),
        TextFormField(
          controller: _passwordController,
          obscureText: _obscurePassword,
          decoration: InputDecoration(
            hintText: 'Shared network password',
            prefixIcon: const Icon(Icons.lock_outlined),
            suffixIcon: IconButton(
              icon: Icon(
                _obscurePassword ? Icons.visibility_outlined : Icons.visibility_off_outlined,
              ),
              onPressed: () {
                setState(() => _obscurePassword = !_obscurePassword);
              },
            ),
          ),
          textInputAction: TextInputAction.next,
          onChanged: (_) => _markChanged(),
          validator: (value) {
            if (value == null || value.isEmpty) {
              return 'Password is required';
            }
            return null;
          },
        ),
      ],
    );
  }

  Widget _buildDomainField() {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          'Server Address',
          style: Theme.of(context).textTheme.titleSmall?.copyWith(
                fontWeight: FontWeight.w600,
              ),
        ),
        const SizedBox(height: 8),
        TextFormField(
          controller: _domainController,
          decoration: const InputDecoration(
            hintText: 'e.g. vpn.example.com',
            prefixIcon: Icon(Icons.dns_outlined),
          ),
          textInputAction: TextInputAction.next,
          onChanged: (_) => _markChanged(),
          validator: (value) {
            if (value == null || value.trim().isEmpty) {
              return 'Server address is required';
            }
            return null;
          },
        ),
      ],
    );
  }

  Widget _buildIpField() {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          'VPN IP (last octet)',
          style: Theme.of(context).textTheme.titleSmall?.copyWith(
                fontWeight: FontWeight.w600,
              ),
        ),
        const SizedBox(height: 8),
        TextFormField(
          controller: _ipController,
          keyboardType: TextInputType.number,
          inputFormatters: [
            FilteringTextInputFormatter.digitsOnly,
          ],
          decoration: const InputDecoration(
            hintText: '2-254',
            prefixIcon: Icon(Icons.numbers_outlined),
            prefixText: '192.168.100.',
          ),
          textInputAction: TextInputAction.done,
          onChanged: (_) => _markChanged(),
          validator: (value) {
            if (value == null || value.isEmpty) {
              return 'IP is required';
            }
            final ip = int.tryParse(value);
            if (ip == null || ip < 2 || ip > 254) {
              return 'Must be between 2 and 254';
            }
            return null;
          },
        ),
      ],
    );
  }

  Widget _buildInfoRow(ThemeData theme, String label, String value) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 12),
      child: Row(
        mainAxisAlignment: MainAxisAlignment.spaceBetween,
        children: [
          Text(
            label,
            style: theme.textTheme.bodyMedium?.copyWith(
              color: theme.textTheme.bodyMedium?.color?.withOpacity(0.6),
            ),
          ),
          Text(
            value,
            style: theme.textTheme.bodyMedium?.copyWith(
              fontWeight: FontWeight.w500,
            ),
          ),
        ],
      ),
    );
  }
}
