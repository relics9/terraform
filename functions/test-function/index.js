// === 修正前（例）===
// Line 42: const value = event.data.someField.nestedProperty;

// === 修正後 ===
exports.testFunction = async (req, res) => {
  try {
    // 入力データのバリデーション
    if (!req.body || !req.body.data) {
      console.warn('Invalid input: request body or data is null/undefined', {
        body: req.body,
        timestamp: new Date().toISOString()
      });
      return res.status(400).json({
        error: 'Bad Request',
        message: 'Request body and data field are required'
      });
    }

    const data = req.body.data;

    // Line 42 付近: null guard を追加
    const someField = data.someField;
    if (!someField || !someField.nestedProperty) {
      console.warn('Missing required field: someField.nestedProperty is null/undefined', {
        receivedData: JSON.stringify(data),
        timestamp: new Date().toISOString()
      });
      return res.status(400).json({
        error: 'Bad Request',
        message: 'someField.nestedProperty is required'
      });
    }

    const value = someField.nestedProperty;

    // ... 以降の処理 ...

    return res.status(200).json({ success: true, value });
  } catch (error) {
    console.error('Unexpected error in test-function:', {
      error: error.message,
      stack: error.stack,
      timestamp: new Date().toISOString()
    });
    return res.status(500).json({
      error: 'Internal Server Error',
      message: 'An unexpected error occurred'
    });
  }
};